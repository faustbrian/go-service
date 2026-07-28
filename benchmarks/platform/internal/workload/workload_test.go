package workload_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

func TestConfiguredDrainWorkRemainsInFlightAfterRequestCancellation(
	t *testing.T,
) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	workload.WaitForDrain(ctx)
	elapsed := time.Since(started)
	if elapsed < workload.DrainWork/2 {
		t.Fatalf(
			"configured drain work = %s, want at least %s",
			elapsed,
			workload.DrainWork/2,
		)
	}
	if elapsed > workload.DrainWork+100*time.Millisecond {
		t.Fatalf(
			"configured drain work = %s, deadline budget = %s",
			elapsed,
			workload.DrainWork+100*time.Millisecond,
		)
	}
}

func TestConfiguredDrainFlushesThroughTheRecoveryWriter(t *testing.T) {
	t.Parallel()

	handler, err := serverhttp.Chain(
		http.HandlerFunc(workload.DrainHTTP),
		serverhttp.Recover(),
	)
	if err != nil {
		t.Fatalf("Chain() error = %v", err)
	}
	writer := &flushWriter{
		header:  make(http.Header),
		flushed: make(chan struct{}),
	}
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(
			writer,
			httptest.NewRequest(http.MethodPost, "/_benchmark/drain", nil),
		)
		close(done)
	}()
	select {
	case <-writer.flushed:
	case <-done:
		t.Fatal("configured drain completed without flushing its response header")
	case <-time.After(time.Second):
		t.Fatal("configured drain did not flush its response header")
	}
	<-done
}

type flushWriter struct {
	header  http.Header
	flushed chan struct{}
	once    sync.Once
}

func (writer *flushWriter) Header() http.Header { return writer.header }

func (*flushWriter) WriteHeader(int) {}

func (*flushWriter) Write(body []byte) (int, error) { return len(body), nil }

func (writer *flushWriter) Flush() {
	writer.once.Do(func() { close(writer.flushed) })
}
