package serverhttp

import (
	"testing"
	"time"
)

const internalSynchronizationTimeout = 2 * time.Second

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
