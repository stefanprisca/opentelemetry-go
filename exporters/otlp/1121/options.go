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
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

const (
	DefaultCollectorPort uint16 = 55680
	DefaultCollectorHost string = "localhost"
	DefaultNumWorkers    uint   = 1

	// For more info on gRPC service configs:
	// https://github.com/grpc/proposal/blob/master/A6-client-retries.md
	//
	// For more info on the RetryableStatusCodes we allow here:
	// https://github.com/open-telemetry/oteps/blob/be2a3fcbaa417ebbf5845cd485d34fdf0ab4a2a4/text/0035-opentelemetry-protocol.md#export-response
	//
	// Note: MaxAttempts > 5 are treated as 5. See
	// https://github.com/grpc/proposal/blob/master/A6-client-retries.md#validation-of-retrypolicy
	// for more details.
	DefaultGRPCServiceConfig = `{
	"methodConfig":[{
		"name":[
			{ "service":"opentelemetry.proto.collector.metrics.v1.MetricsService" },
			{ "service":"opentelemetry.proto.collector.trace.v1.TraceService" }
		],
		"retryPolicy":{
			"MaxAttempts":5,
			"InitialBackoff":"0.3s",
			"MaxBackoff":"5s",
			"BackoffMultiplier":2,
			"RetryableStatusCodes":[
				"UNAVAILABLE",
				"CANCELLED",
				"DEADLINE_EXCEEDED",
				"RESOURCE_EXHAUSTED",
				"ABORTED",
				"OUT_OF_RANGE",
				"UNAVAILABLE",
				"DATA_LOSS"
			]
		}
	}]
}`
)

type ExporterOption func(*compositeConfig)

type compositeConfig struct {
	metrics *config
	traces  *config
}

type config struct {
	canDialInsecure    bool
	collectorAddr      string
	compressor         string
	reconnectionPeriod time.Duration
	grpcServiceConfig  string
	grpcDialOptions    []grpc.DialOption
	headers            map[string]string
	clientCredentials  credentials.TransportCredentials
	numWorkers         uint
}

// WithMetricsAddress allows one to set the address that the metrics exporter will
// connect to the collector on. If unset, it will instead try to use
// connect to DefaultCollectorHost:DefaultCollectorPort.
func WithMetricsAddress(addr string) ExporterOption {
	return func(cfg *compositeConfig) {
		cfg.metrics.collectorAddr = addr
	}
}

// WithMetricsAddress allows one to set the address that the metrics exporter will
// connect to the collector on. If unset, it will instead try to use
// connect to DefaultCollectorHost:DefaultCollectorPort.
func WithMetricsInsecure() ExporterOption {
	return func(cfg *compositeConfig) {
		cfg.metrics.canDialInsecure = true
	}
}

// WithGRPCDialOption opens support to any grpc.DialOption to be used. If it conflicts
// with some other configuration the GRPC specified via the collector the ones here will
// take preference since they are set last.
func WithMetricsGRPCDialOption(opts ...grpc.DialOption) ExporterOption {
	return func(cfg *compositeConfig) {
		cfg.metrics.grpcDialOptions = opts
	}
}

// WithTracesAddress allows one to set the address that the metrics exporter will
// connect to the collector on. If unset, it will instead try to use
// connect to DefaultCollectorHost:DefaultCollectorPort.
func WithTracesAddress(addr string) ExporterOption {
	return func(cfg *compositeConfig) {
		cfg.traces.collectorAddr = addr
	}
}

func WithTracesInsecure() ExporterOption {
	return func(cfg *compositeConfig) {
		cfg.traces.canDialInsecure = true
	}
}

// WithTracesGRPCDialOption opens support to any grpc.DialOption to be used. If it conflicts
// with some other configuration the GRPC specified via the collector the ones here will
// take preference since they are set last.
func WithTracesGRPCDialOption(opts ...grpc.DialOption) ExporterOption {
	return func(cfg *compositeConfig) {
		cfg.traces.grpcDialOptions = opts
	}
}
