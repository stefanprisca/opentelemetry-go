// // Copyright The OpenTelemetry Authors
// //
// // Licensed under the Apache License, Version 2.0 (the "License");
// // you may not use this file except in compliance with the License.
// // You may obtain a copy of the License at
// //
// //     http://www.apache.org/licenses/LICENSE-2.0
// //
// // Unless required by applicable law or agreed to in writing, software
// // distributed under the License is distributed on an "AS IS" BASIS,
// // WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// // See the License for the specific language governing permissions and
// // limitations under the License.

package otlp1121

import (
	"context"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"
	"unsafe"

	"google.golang.org/grpc"
)

type otlpConnection struct {
	lastConnectErrPtr          unsafe.Pointer
	newConnectionHandler       func(cc *grpc.ClientConn) error
	disconnectedCh             chan bool
	backgroundConnectionDoneCh chan bool
	stopCh                     chan bool
	c                          config
}

func newOtlpConnection(handler func(cc *grpc.ClientConn) error, c config) *otlpConnection {
	conn := new(otlpConnection)
	conn.newConnectionHandler = handler
	conn.c = c
	return conn
}

func (oc *otlpConnection) startConnection(stopCh chan bool) {
	oc.stopCh = stopCh

	if err := oc.connect(); err == nil {
		oc.setStateConnected()
	} else {
		oc.setStateDisconnected(err)
	}
	go oc.indefiniteBackgroundConnection()
}

func (oc *otlpConnection) lastConnectError() error {
	errPtr := (*error)(atomic.LoadPointer(&oc.lastConnectErrPtr))
	if errPtr == nil {
		return nil
	}
	return *errPtr
}

func (oc *otlpConnection) saveLastConnectError(err error) {
	var errPtr *error
	if err != nil {
		errPtr = &err
	}
	atomic.StorePointer(&oc.lastConnectErrPtr, unsafe.Pointer(errPtr))
}

func (oc *otlpConnection) setStateDisconnected(err error) {
	oc.saveLastConnectError(err)
	select {
	case oc.disconnectedCh <- true:
	default:
	}
}

func (oc *otlpConnection) setStateConnected() {
	oc.saveLastConnectError(nil)
}

func (oc *otlpConnection) connected() bool {
	return oc.lastConnectError() == nil
}

const defaultConnReattemptPeriod = 10 * time.Second

func (oc *otlpConnection) indefiniteBackgroundConnection() {
	defer func() {
		oc.backgroundConnectionDoneCh <- true
	}()

	connReattemptPeriod := oc.c.reconnectionPeriod
	if connReattemptPeriod <= 0 {
		connReattemptPeriod = defaultConnReattemptPeriod
	}

	// No strong seeding required, nano time can
	// already help with pseudo uniqueness.
	rng := rand.New(rand.NewSource(time.Now().UnixNano() + rand.Int63n(1024)))

	// maxJitterNanos: 70% of the connectionReattemptPeriod
	maxJitterNanos := int64(0.7 * float64(connReattemptPeriod))

	for {
		// Otherwise these will be the normal scenarios to enable
		// reconnection if we trip out.
		// 1. If we've stopped, return entirely
		// 2. Otherwise block until we are disconnected, and
		//    then retry connecting
		select {
		case <-oc.stopCh:
			return

		case <-oc.disconnectedCh:
			// Normal scenario that we'll wait for
		}

		if err := oc.connect(); err == nil {
			oc.setStateConnected()
		} else {
			oc.setStateDisconnected(err)
		}

		// Apply some jitter to avoid lockstep retrials of other
		// collector-exporters. Lockstep retrials could result in an
		// innocent DDOS, by clogging the machine's resources and network.
		jitter := time.Duration(rng.Int63n(maxJitterNanos))
		select {
		case <-oc.stopCh:
			return
		case <-time.After(connReattemptPeriod + jitter):
		}
	}
}

func (oc *otlpConnection) connect() error {
	cc, err := oc.dialToCollector()
	if err != nil {
		return err
	}
	return oc.newConnectionHandler(cc)
}

func (oc *otlpConnection) dialToCollector() (*grpc.ClientConn, error) {
	addr := oc.prepareCollectorAddress()

	dialOpts := []grpc.DialOption{}
	if oc.c.grpcServiceConfig != "" {
		dialOpts = append(dialOpts, grpc.WithDefaultServiceConfig(oc.c.grpcServiceConfig))
	}
	if oc.c.clientCredentials != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(oc.c.clientCredentials))
	} else if oc.c.canDialInsecure {
		dialOpts = append(dialOpts, grpc.WithInsecure())
	}
	if oc.c.compressor != "" {
		dialOpts = append(dialOpts, grpc.WithDefaultCallOptions(grpc.UseCompressor(oc.c.compressor)))
	}
	if len(oc.c.grpcDialOptions) != 0 {
		dialOpts = append(dialOpts, oc.c.grpcDialOptions...)
	}

	ctx := oc.contextWithMetadata(context.Background())
	return grpc.DialContext(ctx, addr, dialOpts...)
}

func (oc *otlpConnection) prepareCollectorAddress() string {
	if oc.c.collectorAddr != "" {
		return oc.c.collectorAddr
	}
	return fmt.Sprintf("%s:%d", DefaultCollectorHost, DefaultCollectorPort)
}

func (oc *otlpConnection) contextWithMetadata(ctx context.Context) context.Context {
	// if me.metadata.Len() > 0 {
	// 	return metadata.NewOutgoingContext(ctx, me.metadata)
	// }
	return ctx
}
