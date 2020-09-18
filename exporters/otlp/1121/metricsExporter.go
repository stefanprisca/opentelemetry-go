// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package otlp1121

import (
	"context"
	"fmt"
	"sync"

	colmetricpb "go.opentelemetry.io/otel/exporters/otlp/internal/opentelemetry-proto-gen/collector/metrics/v1"
	metricsdk "go.opentelemetry.io/otel/sdk/export/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"go.opentelemetry.io/otel/api/metric"
	"go.opentelemetry.io/otel/exporters/otlp/internal/transform"
	"go.opentelemetry.io/otel/sdk/export/metric/aggregation"
)

type metricsExporter struct {
	// mu protects the non-atomic and non-channel variables
	mu             sync.RWMutex
	grpcClientConn *grpc.ClientConn
	metricClient   colmetricpb.MetricsServiceClient
	c              config
	metadata       metadata.MD

	stopCh chan bool
	// senderMu protects the concurrent unsafe sends on the shared gRPC client connection.
	senderMu sync.Mutex
}

func newMetricsExporter(c config) *metricsExporter {
	me := new(metricsExporter)
	me.c = c
	return me
}

func (me *metricsExporter) Connect() error {
	me.stopCh = make(chan bool)
	cc, err := me.dialToCollector()
	if err != nil {
		return err
	}

	err = me.enableConnection(cc)
	if err != nil {
		return err
	}

	return nil
}

func (me *metricsExporter) prepareCollectorAddress() string {
	if me.c.collectorAddr != "" {
		return me.c.collectorAddr
	}
	return fmt.Sprintf("%s:%d", DefaultCollectorHost, DefaultCollectorPort)
}

func (me *metricsExporter) contextWithMetadata(ctx context.Context) context.Context {
	if me.metadata.Len() > 0 {
		return metadata.NewOutgoingContext(ctx, me.metadata)
	}
	return ctx
}

func (me *metricsExporter) dialToCollector() (*grpc.ClientConn, error) {
	addr := me.prepareCollectorAddress()

	dialOpts := []grpc.DialOption{}
	if me.c.grpcServiceConfig != "" {
		dialOpts = append(dialOpts, grpc.WithDefaultServiceConfig(me.c.grpcServiceConfig))
	}
	if me.c.clientCredentials != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(me.c.clientCredentials))
	} else if me.c.canDialInsecure {
		dialOpts = append(dialOpts, grpc.WithInsecure())
	}
	if me.c.compressor != "" {
		dialOpts = append(dialOpts, grpc.WithDefaultCallOptions(grpc.UseCompressor(me.c.compressor)))
	}
	if len(me.c.grpcDialOptions) != 0 {
		dialOpts = append(dialOpts, me.c.grpcDialOptions...)
	}

	ctx := me.contextWithMetadata(context.Background())
	return grpc.DialContext(ctx, addr, dialOpts...)
}

func (me *metricsExporter) enableConnection(cc *grpc.ClientConn) error {

	me.mu.Lock()
	// If previous clientConn is same as the current then just return.
	// This doesn't happen right now as this func is only called with new ClientConn.
	// It is more about future-proofing.
	if me.grpcClientConn == cc {
		me.mu.Unlock()
		return nil
	}
	// If the previous clientConn was non-nil, close it
	if me.grpcClientConn != nil {
		_ = me.grpcClientConn.Close()
	}
	me.grpcClientConn = cc
	me.metricClient = colmetricpb.NewMetricsServiceClient(cc)
	me.mu.Unlock()

	return nil
}

func (me *metricsExporter) ExportKindFor(*metric.Descriptor, aggregation.Kind) metricsdk.ExportKind {
	return metricsdk.PassThroughExporter
}

func (me *metricsExporter) Export(parent context.Context, cps metricsdk.CheckpointSet) error {
	// Unify the parent context Done signal with the exporter stopCh.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func(ctx context.Context, cancel context.CancelFunc) {
		select {
		case <-ctx.Done():
		case <-me.stopCh:
			cancel()
		}
	}(ctx, cancel)

	rms, err := transform.CheckpointSet(ctx, me, cps, me.c.numWorkers)
	if err != nil {
		return err
	}

	// if !me.connected() {
	// 	return errDisconnected
	// }

	select {
	case <-me.stopCh:
		return errStopped
	case <-ctx.Done():
		return errContextCanceled
	default:
		me.senderMu.Lock()
		_, err := me.metricClient.Export(me.contextWithMetadata(ctx), &colmetricpb.ExportMetricsServiceRequest{
			ResourceMetrics: rms,
		})
		me.senderMu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}
