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

package otlp1121_test

import (
	"context"
	"fmt"
	"log"
	"testing"
	"time"

	"go.opentelemetry.io/otel/api/global"
	"go.opentelemetry.io/otel/api/metric"
	otlp1121 "go.opentelemetry.io/otel/exporters/otlp/1121"
	"go.opentelemetry.io/otel/sdk/metric/controller/push"
	"go.opentelemetry.io/otel/sdk/metric/processor/basic"
	"go.opentelemetry.io/otel/sdk/metric/selector/simple"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/semconv"
	"google.golang.org/grpc"
)

func TestExporterConfig(t *testing.T) {
	exp, err := otlp1121.NewExporter(
		otlp1121.WithMetricsAddress("localhost:30080"),
		otlp1121.WithMetricsInsecure(),
		otlp1121.WithMetricsGRPCDialOption(grpc.WithBlock()),
		otlp1121.WithTracesAddress("localhost:30080"),
		otlp1121.WithTracesInsecure(),
		otlp1121.WithTracesGRPCDialOption(grpc.WithBlock()))
	if err != nil {
		log.Fatalf("Failed to create the collector exporter: %v", err)
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := exp.Shutdown(ctx); err != nil {
			global.Handle(err)
		}
	}()

	tracerProvider := sdktrace.NewProvider(
		sdktrace.WithConfig(sdktrace.Config{DefaultSampler: sdktrace.AlwaysSample()}),
		sdktrace.WithResource(resource.New(
			// the service name used to display traces in backends
			semconv.ServiceNameKey.String("test-service"),
		)),
		sdktrace.WithBatcher(exp),
	)

	pusher := push.New(
		basic.New(
			simple.NewWithExactDistribution(),
			exp,
		),
		exp,
		push.WithPeriod(time.Second),
	)
	global.SetMeterProvider(pusher.Provider())
	global.SetTracerProvider(tracerProvider)
	pusher.Start()

	meter := global.Meter("test-meter")

	// Recorder metric example
	valuerecorder := metric.Must(meter).
		NewFloat64Counter(
			"an_important_metric",
			metric.WithDescription("Measures the cumulative epicness of the app"),
		)

	tracer := global.Tracer("test-tracer")
	ctx, span := tracer.Start(
		context.Background(),
		"CollectorExporter-Example")

	// work begins

	for i := 0; i < 10; i++ {
		_, iSpan := tracer.Start(ctx, fmt.Sprintf("Sample-%d", i))
		log.Printf("Doing really hard work (%d / 10)\n", i+1)
		valuerecorder.Add(context.Background(), 1.0)

		<-time.After(time.Second)
		iSpan.End()
	}

	log.Printf("Done!")
	span.End()
}
