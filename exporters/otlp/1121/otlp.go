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
	metricsdk "go.opentelemetry.io/otel/sdk/export/metric"
	"go.opentelemetry.io/otel/sdk/export/metric/aggregation"
	tracesdk "go.opentelemetry.io/otel/sdk/export/trace"
)

type Exporter struct {
	// mu protects the non-atomic and non-channel variables
	mu      sync.RWMutex
	started bool

	metricExporter *metricsExporter

	startOnce sync.Once
	stopCh    chan bool

	c compositeConfig
}

var _ tracesdk.SpanExporter = (*Exporter)(nil)
var _ metricsdk.Exporter = (*metricsExporter)(nil)

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
	e.c = newConfig(opts...)
	e.metricExporter = newMetricsExporter(*e.c.metrics)

	// TODO (sprisca): what happens to the headers?
	// if len(e.c.headers) > 0 {
	// 	e.metadata = metadata.New(e.c.headers)
	// }

	// TODO (rghetia): add resources

	return e
}

var (
	errAlreadyStarted  = errors.New("already started")
	errNotStarted      = errors.New("not started")
	errDisconnected    = errors.New("exporter disconnected")
	errStopped         = errors.New("exporter stopped")
	errContextCanceled = errors.New("context canceled")
)

// Start dials to the collector, establishing a connection to it. It also
// initiates the Config and Trace services by sending over the initial
// messages that consist of the node identifier. Start invokes a background
// connector that will reattempt connections to the collector periodically
// if the connection dies.
func (e *Exporter) Start() error {
	var err = errAlreadyStarted
	e.startOnce.Do(func() {
		e.mu.Lock()
		e.started = true
		// e.disconnectedCh = make(chan bool, 1)
		// e.backgroundConnectionDoneCh = make(chan bool)
		e.stopCh = make(chan bool)
		e.mu.Unlock()

		e.connectExporters()

		// go e.indefiniteBackgroundConnection()

		err = nil
	})

	return err
}

func (e *Exporter) connectExporters() error {
	e.mu.RLock()
	started := e.started
	e.mu.RUnlock()
	if !started {
		return errNotStarted
	}

	log.Println("connecting exporters....")
	e.metricExporter.Connect()
	return nil
}

// closeStopCh is used to wrap the exporters stopCh channel closing for testing.
var closeStopCh = func(stopCh chan bool) {
	close(stopCh)
}

// Shutdown closes all connections and releases resources currently being used
// by the exporter. If the exporter is not started this does nothing.
func (e *Exporter) Shutdown(ctx context.Context) error {
	// e.mu.RLock()
	// cc := e.grpcClientConn
	// started := e.started
	// e.mu.RUnlock()

	// if !started {
	// 	return nil
	// }

	// var err error
	// if cc != nil {
	// 	// Clean things up before checking this error.
	// 	err = cc.Close()
	// }

	// // At this point we can change the state variable started
	// e.mu.Lock()
	// e.started = false
	// e.mu.Unlock()
	// closeStopCh(e.stopCh)

	// // Ensure that the backgroundConnector returns
	// select {
	// case <-e.backgroundConnectionDoneCh:
	// case <-ctx.Done():
	// 	return ctx.Err()
	// }

	return nil
}

// Export implements the "go.opentelemetry.io/otel/sdk/export/metric".Exporter
// interface. It transforms and batches metric Records into OTLP Metrics and
// transmits them to the configured collector.
func (e *Exporter) Export(parent context.Context, cps metricsdk.CheckpointSet) error {
	log.Println("exporting metrics....")
	return e.metricExporter.Export(parent, cps)
}

func (e *Exporter) ExportSpans(ctx context.Context, sds []*tracesdk.SpanData) error {
	log.Println("exporting spans....")
	return e.uploadTraces(ctx, sds)
}

func (e *Exporter) uploadTraces(ctx context.Context, sdl []*tracesdk.SpanData) error {
	// select {
	// case <-e.stopCh:
	// 	return nil
	// default:
	// 	if !e.connected() {
	// 		return nil
	// 	}

	// 	protoSpans := transform.SpanData(sdl)
	// 	if len(protoSpans) == 0 {
	// 		return nil
	// 	}

	// 	e.senderMu.Lock()
	// 	_, err := e.traceExporter.Export(e.contextWithMetadata(ctx), &coltracepb.ExportTraceServiceRequest{
	// 		ResourceSpans: protoSpans,
	// 	})
	// 	e.senderMu.Unlock()
	// 	if err != nil {
	// 		e.setStateDisconnected(err)
	// 		return err
	// 	}
	// }
	return nil
}
