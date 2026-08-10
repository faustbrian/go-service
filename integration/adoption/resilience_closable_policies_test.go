package adoption_test

import (
	"context"
	"errors"
	"testing"
	"time"

	ratelimit "github.com/faustbrian/golib/pkg/rate-limit"
	ratelimitmemory "github.com/faustbrian/golib/pkg/rate-limit/memory"
	"github.com/faustbrian/golib/pkg/semaphore"
	"github.com/faustbrian/golib/pkg/service"
	serviceintegration "github.com/faustbrian/golib/pkg/service/integration"
)

func TestClosableSemaphoreAndRateStoreFollowServiceLifecycle(t *testing.T) {
	t.Parallel()

	queued := make(chan struct{}, 1)
	shared, err := semaphore.New(semaphore.Config{
		Capacity:   1,
		MaxWaiters: 1,
		Observer: semaphore.ObserverFunc(func(event semaphore.Event) {
			if event.Kind == semaphore.EventQueued {
				queued <- struct{}{}
			}
		}),
	})
	if err != nil {
		t.Fatalf("semaphore.New() error = %v", err)
	}
	store, err := ratelimitmemory.New(ratelimitmemory.Options{MaxKeys: 1, Shards: 1})
	if err != nil {
		t.Fatalf("memory.New() error = %v", err)
	}
	component, err := serviceintegration.New("shared-admission", serviceintegration.Hooks{
		CloseAdmission: func() error {
			return errors.Join(shared.Close(), store.Close())
		},
		Stop: shared.Wait,
	})
	if err != nil {
		t.Fatalf("integration.New() error = %v", err)
	}
	runtime, err := service.New(service.Config{Components: []service.Component{component}})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	holder, err := shared.Acquire(context.Background(), 1)
	if err != nil {
		t.Fatalf("holder Acquire() error = %v", err)
	}
	waiter := make(chan error, 1)
	go func() {
		_, acquireErr := shared.Acquire(context.Background(), 1)
		waiter <- acquireErr
	}()
	<-queued

	policy, err := ratelimit.NewPolicy(ratelimit.PolicySpec{
		ID: "inventory", Revision: "v1", Algorithm: ratelimit.TokenBucket,
		Capacity: 1, Period: time.Second, FailureMode: ratelimit.FailClosed,
	})
	if err != nil {
		t.Fatalf("ratelimit.NewPolicy() error = %v", err)
	}
	key, err := ratelimit.NewKey(ratelimit.KeySpec{
		Namespace: "service", Version: "v1",
		Subject: ratelimit.Subject{Kind: "dependency", Value: "inventory"},
	})
	if err != nil {
		t.Fatalf("ratelimit.NewKey() error = %v", err)
	}
	request := ratelimit.Request{
		Policy: policy, Key: key, Cost: 1,
		Now: time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC),
	}
	if decision, err := store.Admit(context.Background(), request); err != nil || !decision.Allowed {
		t.Fatalf("Admit() = (%+v, %v), want allowed", decision, err)
	}

	if err := runtime.Drain(); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if err := <-waiter; !errors.Is(err, semaphore.ErrClosed) {
		t.Fatalf("queued semaphore error = %v, want ErrClosed", err)
	}
	if _, err := shared.Acquire(context.Background(), 1); !errors.Is(err, semaphore.ErrClosed) {
		t.Fatalf("new semaphore admission error = %v, want ErrClosed", err)
	}
	request.Now = request.Now.Add(time.Second)
	if _, err := store.Admit(context.Background(), request); !errors.Is(err, ratelimit.ErrUnavailable) {
		t.Fatalf("rate store after drain error = %v, want ErrUnavailable", err)
	}
	if snapshot := shared.Snapshot(); !snapshot.Closed || snapshot.Waiters != 0 || snapshot.Acquired != 1 {
		t.Fatalf("semaphore drain snapshot = %+v", snapshot)
	}

	if err := holder.Release(); err != nil {
		t.Fatalf("holder Release() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("repeated Shutdown() error = %v", err)
	}
}
