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

// code in this package is mostly copied from contrib.go.opencensus.io/exporter/ocagent/connection.go
package otlp1121

import (
	"context"
	"errors"
	"log"
	"sync"

	"go.opentelemetry.io/otel/api/metric"
	colmetricpb "go.opentelemetry.io/otel/exporters/otlp/internal/opentelemetry-proto-gen/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/otel/exporters/otlp/internal/opentelemetry-proto-gen/collector/trace/v1"
	"go.opentelemetry.io/otel/exporters/otlp/internal/transform"

	metricsdk "go.opentelemetry.io/otel/sdk/export/metric"
	"go.opentelemetry.io/otel/sdk/export/metric/aggregation"
	tracesdk "go.opentelemetry.io/otel/sdk/export/trace"
	"google.golang.org/grpc"
)

type Exporter struct {
	// mu protects the non-atomic and non-channel variables
	mu       sync.RWMutex
	senderMu sync.RWMutex
	started  bool

	metricsConnection *otlpConnection
	metricsClient     colmetricpb.MetricsServiceClient

	tracesConnection *otlpConnection
	tracesClient     coltracepb.TraceServiceClient

	startOnce sync.Once
	stopCh    chan bool
}

var _ tracesdk.SpanExporter = (*Exporter)(nil)
var _ metricsdk.Exporter = (*Exporter)(nil)

func (e *Exporter) ExportKindFor(*metric.Descriptor, aggregation.Kind) metricsdk.ExportKind {
	return metricsdk.PassThroughExporter
}

// newConfig initializes a config struct with default values and applies
// any ExporterOptions provided.
func newConfig(opts ...ExporterOption) compositeConfig {
	metricsCfg := &config{
		numWorkers:        DefaultNumWorkers,
		grpcServiceConfig: DefaultGRPCServiceConfig,
	}
	tracesCfg := &config{
		numWorkers:        DefaultNumWorkers,
		grpcServiceConfig: DefaultGRPCServiceConfig,
	}

	compCfg := compositeConfig{
		metrics: metricsCfg,
		traces:  tracesCfg,
	}

	for _, opt := range opts {
		opt(&compCfg)
	}
	return compCfg
}

// NewExporter constructs a new Exporter and starts it.
func NewExporter(opts ...ExporterOption) (*Exporter, error) {
	exp := NewUnstartedExporter(opts...)
	if err := exp.Start(); err != nil {
		return nil, err
	}
	return exp, nil
}

// NewUnstartedExporter constructs a new Exporter and does not start it.
func NewUnstartedExporter(opts ...ExporterOption) *Exporter {
	log.Println("creating exporter....")
	e := new(Exporter)
	config := newConfig(opts...)

	e.metricsConnection = newOtlpConnection(e.handleNewMetricsConnection, *config.metrics)
	e.tracesConnection = newOtlpConnection(e.handleNewTracesConnection, *config.traces)

	// TODO (rghetia): add resources

	return e
}

func (e *Exporter) handleNewMetricsConnection(cc *grpc.ClientConn) error {

	e.mu.Lock()
	e.metricsClient = colmetricpb.NewMetricsServiceClient(cc)
	e.mu.Unlock()

	return nil
}

func (e *Exporter) handleNewTracesConnection(cc *grpc.ClientConn) error {

	e.mu.Lock()
	e.tracesClient = coltracepb.NewTraceServiceClient(cc)
	e.mu.Unlock()

	return nil
}

var (
	errAlreadyStarted  = errors.New("already started")
	errNotStarted      = errors.New("not started")
	errDisconnected    = errors.New("exporter disconnected")
	errStopped         = errors.New("exporter stopped")
	errContextCanceled = errors.New("context canceled")
)

func (e *Exporter) Start() error {
	var err = errAlreadyStarted
	e.startOnce.Do(func() {
		e.mu.Lock()
		e.started = true
		e.stopCh = make(chan bool)
		e.mu.Unlock()

		e.startExporterConnections(e.stopCh)
		err = nil
	})

	return err
}

func (e *Exporter) startExporterConnections(stopCh chan bool) error {
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return errNotStarted
	}

	e.metricsConnection.startConnection(stopCh)
	e.tracesConnection.startConnection(stopCh)
	return nil
}

// closeStopCh is used to wrap the exporters stopCh channel closing for testing.
var closeStopCh = func(stopCh chan bool) {
	close(stopCh)
}

// Shutdown closes all connections and releases resources currently being used
// by the exporter. If the exporter is not started this does nothing.
func (e *Exporter) Shutdown(ctx context.Context) error {
	e.mu.RLock()
	metricsConn := e.metricsConnection
	tracesConn := e.tracesConnection
	started := e.started
	e.mu.RUnlock()

	if !started {
		return nil
	}

	closeStopCh(e.stopCh)

	var err error
	if metricsConn != nil {
		// Clean things up before checking this error.
		err = metricsConn.shutdown(ctx)
	}
	if err != nil {
		return err
	}

	if tracesConn != nil {
		// Clean things up before checking this error.
		err = tracesConn.shutdown(ctx)
	}
	if err != nil {
		return err
	}

	// At this point we can change the state variable started
	e.mu.Lock()
	e.started = false
	e.mu.Unlock()

	return err
}

// Export implements the "go.opentelemetry.io/otel/sdk/export/metric".Exporter
// interface. It transforms and batches metric Records into OTLP Metrics and
// transmits them to the configured collector.
func (e *Exporter) Export(parent context.Context, cps metricsdk.CheckpointSet) error {
	// Unify the parent context Done signal with the exporter stopCh.
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func(ctx context.Context, cancel context.CancelFunc) {
		select {
		case <-ctx.Done():
		case <-e.stopCh:
			cancel()
		}
	}(ctx, cancel)

	numWorkers := e.metricsConnection.c.numWorkers
	rms, err := transform.CheckpointSet(ctx, e, cps, numWorkers)
	if err != nil {
		return err
	}

	if !e.metricsConnection.connected() {
		return errDisconnected
	}

	select {
	case <-e.stopCh:
		return errStopped
	case <-ctx.Done():
		return errContextCanceled
	default:
		e.senderMu.Lock()
		metricsContext := e.metricsConnection.contextWithMetadata(ctx)
		_, err := e.metricsClient.Export(metricsContext, &colmetricpb.ExportMetricsServiceRequest{
			ResourceMetrics: rms,
		})
		e.senderMu.Unlock()
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Exporter) ExportSpans(ctx context.Context, sds []*tracesdk.SpanData) error {
	return e.uploadTraces(ctx, sds)
}

func (e *Exporter) uploadTraces(ctx context.Context, sdl []*tracesdk.SpanData) error {
	select {
	case <-e.stopCh:
		return nil
	default:
		if !e.tracesConnection.connected() {
			return nil
		}

		protoSpans := transform.SpanData(sdl)
		if len(protoSpans) == 0 {
			return nil
		}

		e.senderMu.Lock()
		tracesCtx := e.tracesConnection.contextWithMetadata(ctx)
		_, err := e.tracesClient.Export(tracesCtx, &coltracepb.ExportTraceServiceRequest{
			ResourceSpans: protoSpans,
		})
		e.senderMu.Unlock()
		if err != nil {
			e.tracesConnection.setStateDisconnected(err)
			return err
		}
	}
	return nil
}
