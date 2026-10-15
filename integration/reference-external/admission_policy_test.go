package referenceexternal_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/go-bulkhead"
	filesystemmemory "github.com/faustbrian/go-filesystem/v2/memory"
	referenceexternal "github.com/faustbrian/go-service/integration/reference-external"
	"github.com/faustbrian/go-webhook/v3"
)

func TestReferenceFetchBoundsAdmissionAndRecoversAfterCancellation(t *testing.T) {
	t.Parallel()
	var calls, active, peak atomic.Int64
	admitted, heldDone := make(chan struct{}, 8), make(chan struct{}, 8)
	released := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(released) }) }
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		current := active.Add(1)
		held := strings.HasPrefix(request.URL.Path, "/held/")
		defer func() {
			active.Add(-1)
			if held {
				heldDone <- struct{}{}
			}
		}()
		for old := peak.Load(); current > old && !peak.CompareAndSwap(old, current); old = peak.Load() {
		}
		if held {
			admitted <- struct{}{}
			select {
			case <-request.Context().Done():
				return
			case <-released:
			}
		}
		_, _ = writer.Write([]byte("ok"))
	}))
	t.Cleanup(upstream.Close)
	reference := newReference(t, upstream.URL, filesystemmemory.New())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := reference.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	type outcome struct {
		index  int
		result referenceexternal.FetchResult
		err    error
	}
	results := make(chan outcome, 8)
	cancels := make([]context.CancelFunc, 8)
	for index := range 8 {
		callContext, stop := context.WithCancel(ctx)
		cancels[index] = stop
		go func() {
			result, err := reference.Fetch(callContext, upstream.URL+"/held/"+string(rune('a'+index)))
			results <- outcome{index: index, result: result, err: err}
		}()
	}
	t.Cleanup(func() {
		for _, stop := range cancels {
			stop()
		}
	})
	await := func(signal <-chan struct{}) {
		t.Helper()
		select {
		case <-signal:
		case <-ctx.Done():
			t.Fatal("held physical request did not reach its boundary")
		}
	}
	nextResult := func() outcome {
		t.Helper()
		select {
		case result := <-results:
			return result
		case <-ctx.Done():
			t.Fatal("admitted Fetch did not finish")
			return outcome{}
		}
	}
	for range 8 {
		await(admitted)
	}
	rejected, err := reference.Fetch(ctx, upstream.URL+"/excess")
	if !errors.Is(err, bulkhead.ErrRejected) || rejected.Attempts != 0 || calls.Load() != 8 || rejected.Bulkhead.ActiveWeight != 8 || rejected.Concurrency.InFlight != 8 {
		t.Fatalf("excess Fetch escaped admission: error=%v attempts=%d physical=%d bulkhead=%d concurrency=%d", err, rejected.Attempts, calls.Load(), rejected.Bulkhead.ActiveWeight, rejected.Concurrency.InFlight)
	}

	cancels[0]()
	canceled := nextResult()
	if canceled.index != 0 || !errors.Is(canceled.err, context.Canceled) || canceled.result.Attempts != 1 {
		t.Fatalf("held cancellation outcome: index=%d error=%v attempts=%d", canceled.index, canceled.err, canceled.result.Attempts)
	}
	await(heldDone)
	// Cancellation and local rejection are ignored by the pinned adaptive
	// classifier. They do not create a probabilistic downstream-overload sample.
	recovered, err := reference.Fetch(ctx, upstream.URL+"/recovery")
	if err != nil || string(recovered.Body) != "ok" || recovered.Attempts != 1 || recovered.Bulkhead.ActiveWeight != 7 || recovered.Concurrency.InFlight != 7 || calls.Load() != 9 {
		t.Fatalf("same-instance recovery did not reuse the released permit: error=%v result=%+v physical=%d", err, recovered, calls.Load())
	}
	release()
	for range 7 {
		completed := nextResult()
		if completed.err != nil || completed.result.Attempts != 1 || string(completed.result.Body) != "ok" {
			t.Fatalf("released Fetch did not finish once: error=%v attempts=%d", completed.err, completed.result.Attempts)
		}
		await(heldDone)
	}
	final, err := reference.Fetch(ctx, upstream.URL+"/healthy")
	if err != nil || string(final.Body) != "ok" || final.Attempts != 1 || calls.Load() != 10 || active.Load() != 0 || peak.Load() != 8 || final.Bulkhead.ActiveWeight != 0 || final.Concurrency.InFlight != 0 || final.Concurrency.Queued != 0 {
		t.Fatalf("drained instance did not recover within its resource/attempt bounds: error=%v physical=%d active=%d peak=%d result=%+v", err, calls.Load(), active.Load(), peak.Load(), final)
	}
}

func TestReferenceWebhookEndpointPolicyPrecedesTransportAndSignedDelivery(t *testing.T) {
	t.Parallel()
	var receiverCalls, transportCalls, policyCalls atomic.Int64
	receiver := newWebhookReceiver(t, &receiverCalls)
	t.Cleanup(receiver.Close)
	var allowed atomic.Bool
	privatePolicyError := errors.New("synthetic-private-policy-detail")
	policy := webhook.EndpointPolicyFunc(func(_ context.Context, endpoint *url.URL) error {
		policyCalls.Add(1)
		if endpoint.String() != receiver.URL || !allowed.Load() {
			return privatePolicyError
		}
		return nil
	})
	reference, err := referenceexternal.New(validConfigWithPolicy(t, receiver.URL, filesystemmemory.New(), policy))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reference.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	ctx := httptrace.WithClientTrace(t.Context(), &httptrace.ClientTrace{
		GetConn: func(string) { transportCalls.Add(1) },
	})
	denied, err := reference.DeliverWebhook(ctx, []byte(`{"event":"ready"}`), "denied")
	if !errors.Is(err, webhook.ErrEndpointRejected) || len(denied.Attempts) != 0 || policyCalls.Load() != 1 || transportCalls.Load() != 0 || receiverCalls.Load() != 0 {
		t.Fatal("denied webhook reached transport/receiver or lost its policy rejection")
	}
	if strings.Contains(err.Error(), privatePolicyError.Error()) {
		t.Fatal("endpoint denial disclosed private policy detail")
	}
	allowed.Store(true)
	delivered, err := reference.DeliverWebhook(ctx, []byte(`{"event":"ready"}`), "allowed")
	if err != nil || len(delivered.Attempts) != 1 || delivered.Attempts[0].StatusCode != http.StatusNoContent || policyCalls.Load() != 2 || transportCalls.Load() != 1 || receiverCalls.Load() != 1 {
		t.Fatalf("allowed signed delivery did not complete exactly once: error=%v attempts=%d transport=%d verified=%d", err, len(delivered.Attempts), transportCalls.Load(), receiverCalls.Load())
	}
	var fetchCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		fetchCalls.Add(1)
		_, _ = writer.Write([]byte("trusted-fetch"))
	}))
	t.Cleanup(upstream.Close)
	allowed.Store(false)
	fetched, err := reference.Fetch(ctx, upstream.URL)
	if err != nil || string(fetched.Body) != "trusted-fetch" || fetched.Attempts != 1 || fetchCalls.Load() != 1 || transportCalls.Load() != 2 || policyCalls.Load() != 2 {
		t.Fatal("trusted Fetch was incorrectly conflated with webhook destination policy")
	}
}
