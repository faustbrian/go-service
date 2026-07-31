package servicetest_test

import (
	"testing"
	"time"
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
