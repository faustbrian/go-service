package service_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service"
)

const synchronizationTimeout = 2 * time.Second

func receiveTestValue[T any](t *testing.T, values <-chan T) T {
	t.Helper()

	timer := time.NewTimer(synchronizationTimeout)
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
	definition service.Definition,
	invocation service.Invocation,
) int {
	t.Helper()

	result := make(chan int, 1)
	go func() {
		result <- service.Execute(ctx, definition, invocation)
	}()

	return receiveTestValue(t, result)
}

func shutdownTest(
	t *testing.T,
	runtime *service.Service,
	ctx context.Context,
) error {
	t.Helper()

	result := make(chan error, 1)
	go func() {
		result <- runtime.Shutdown(ctx)
	}()

	return receiveTestValue(t, result)
}

func startTest(
	t *testing.T,
	runtime *service.Service,
	ctx context.Context,
) error {
	t.Helper()

	result := make(chan error, 1)
	go func() {
		result <- runtime.Start(ctx)
	}()

	return receiveTestValue(t, result)
}

func requireTestCondition(t *testing.T, description string, condition func() bool) {
	t.Helper()

	timer := time.NewTimer(synchronizationTimeout)
	defer timer.Stop()

	for {
		if condition() {
			return
		}

		select {
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", description)
		default:
			runtime.Gosched()
		}
	}
}
