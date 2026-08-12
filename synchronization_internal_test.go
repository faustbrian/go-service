package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

const internalSynchronizationTimeout = 2 * time.Second

type observedDoneContext struct {
	context.Context
	observed chan<- struct{}
}

func (ctx observedDoneContext) Done() <-chan struct{} {
	ctx.observed <- struct{}{}

	return ctx.Context.Done()
}

func receiveTestValue[T any](t *testing.T, values <-chan T) T {
	t.Helper()

	timer := time.NewTimer(internalSynchronizationTimeout)
	defer timer.Stop()

	select {
	case value := <-values:
		return value
	case <-timer.C:
		t.Fatal("timed out waiting for test synchronization")

		var zero T

		return zero
	}
}

func executeTest(
	t *testing.T,
	ctx context.Context,
	definition Definition,
	invocation Invocation,
) int {
	t.Helper()

	result := make(chan int, 1)
	go func() {
		result <- Execute(ctx, definition, invocation)
	}()

	return receiveTestValue(t, result)
}

func TestStartupShutdownWaiterJoinsRollback(t *testing.T) {
	shutdownFailure := errors.New("rollback failed")
	runtime := &Service{
		state:        StateStopping,
		shutdownDone: make(chan struct{}),
	}
	startupDone := make(chan struct{})
	close(startupDone)
	doneObserved := make(chan struct{}, 2)
	ctx := observedDoneContext{Context: context.Background(), observed: doneObserved}

	result := make(chan error, 1)
	go func() {
		result <- runtime.waitForStartupShutdown(ctx, startupDone)
	}()
	receiveTestValue(t, doneObserved)
	receiveTestValue(t, doneObserved)

	select {
	case err := <-result:
		t.Fatalf("waitForStartupShutdown() returned before rollback: %v", err)
	default:
	}

	runtime.mu.Lock()
	runtime.shutdownErr = shutdownFailure
	close(runtime.shutdownDone)
	runtime.mu.Unlock()

	if err := receiveTestValue(t, result); !errors.Is(err, shutdownFailure) {
		t.Fatalf("waitForStartupShutdown() error = %v, want rollback failure", err)
	}
}
