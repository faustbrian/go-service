package referencerecipes_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	queueservice "github.com/faustbrian/go-queue/adapters/service"
	queuecore "github.com/faustbrian/go-queue/core"
	"github.com/faustbrian/go-queue/job"
)

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()

	listener, err := (&net.ListenConfig{}).Listen(
		context.Background(),
		"tcp",
		"127.0.0.1:0",
	)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	return listener
}

func assertHTTPResponse(
	t *testing.T,
	ctx context.Context,
	endpoint string,
	wantStatus int,
	wantBody string,
) {
	t.Helper()

	client := &http.Client{
		Timeout: 250 * time.Millisecond,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	t.Cleanup(client.CloseIdleConnections)

	var lastStatus int
	var lastBody string
	var lastErr error
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		response, err := client.Do(request)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
			closeErr := response.Body.Close()
			lastStatus = response.StatusCode
			lastBody = string(body)
			lastErr = errors.Join(readErr, closeErr)
			if lastErr == nil && lastStatus == wantStatus &&
				(wantBody == "" || lastBody == wantBody) {
				return
			}
		} else {
			lastErr = err
		}

		select {
		case <-ctx.Done():
			t.Fatalf(
				"GET %s = (%d, %q, %v), want (%d, %q)",
				endpoint,
				lastStatus,
				lastBody,
				lastErr,
				wantStatus,
				wantBody,
			)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type testPayload string

func (payload testPayload) Bytes() []byte { return []byte(payload) }

type testDelivery struct {
	body         []byte
	metadata     *job.Metadata
	acknowledged chan<- struct{}
	ackOnce      sync.Once
}

func (delivery *testDelivery) Bytes() []byte   { return append([]byte(nil), delivery.body...) }
func (delivery *testDelivery) Payload() []byte { return append([]byte(nil), delivery.body...) }

func (delivery *testDelivery) CorrelationMetadata() map[string]string {
	if delivery.metadata == nil {
		return nil
	}

	metadata := make(map[string]string, len(delivery.metadata.Correlation))
	for key, value := range delivery.metadata.Correlation {
		metadata[key] = value
	}

	return metadata
}

func (*testDelivery) AcknowledgementRequired() bool { return true }

func (delivery *testDelivery) Ack() error {
	delivery.ackOnce.Do(func() { delivery.acknowledged <- struct{}{} })

	return nil
}

func (*testDelivery) Nack() error { return nil }

type testBroker struct {
	deliveries   chan *testDelivery
	acknowledged chan struct{}
	stopped      chan struct{}
	accepting    atomic.Bool
	shutdownOnce sync.Once
}

func newTestBroker(capacity int) *testBroker {
	broker := &testBroker{
		deliveries:   make(chan *testDelivery, capacity),
		acknowledged: make(chan struct{}, capacity),
		stopped:      make(chan struct{}),
	}
	broker.accepting.Store(true)

	return broker
}

func (broker *testBroker) publish(
	ctx context.Context,
	message queuecore.QueuedMessage,
	options ...job.AllowOption,
) (queueservice.PublishAcceptance, error) {
	if !broker.accepting.Load() {
		return queueservice.PublishNotAccepted, queueservice.ErrUnavailable
	}
	if cause := context.Cause(ctx); cause != nil {
		return queueservice.PublishNotAccepted, cause
	}

	var metadata *job.Metadata
	if len(options) > 0 {
		metadata = options[0].Metadata
	}
	delivery := &testDelivery{
		body:         append([]byte(nil), message.Bytes()...),
		metadata:     metadata,
		acknowledged: broker.acknowledged,
	}
	select {
	case broker.deliveries <- delivery:
		return queueservice.PublishAccepted, nil
	case <-ctx.Done():
		return queueservice.PublishNotAccepted, context.Cause(ctx)
	}
}

func (broker *testBroker) closeAdmission() error {
	broker.accepting.Store(false)

	return nil
}

func (broker *testBroker) run(
	ctx context.Context,
	handler queueservice.Handler,
) error {
	for {
		select {
		case delivery := <-broker.deliveries:
			if err := handler(ctx, delivery); err != nil {
				_ = delivery.Nack()

				return err
			}
			if err := delivery.Ack(); err != nil {
				return err
			}
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

func (broker *testBroker) shutdown(context.Context) error {
	broker.shutdownOnce.Do(func() { close(broker.stopped) })

	return nil
}
