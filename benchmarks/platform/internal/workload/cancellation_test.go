package workload_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/faustbrian/go-correlation"
	"github.com/faustbrian/go-service/benchmarks/platform/internal/workload"
)

func TestCancellationFixtureObservesDisconnectedRequestAndJoins(t *testing.T) {
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(workload.NewMux(factory))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/_benchmark/cancel?mode=disconnect", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		response, callErr := server.Client().Do(request)
		if callErr == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
	deadline := time.Now().Add(time.Second)
	for {
		response, callErr := server.Client().Get(server.URL + "/_benchmark/cancel/status?mode=disconnect")
		if callErr != nil {
			t.Fatal(callErr)
		}
		if response.StatusCode != http.StatusOK {
			_ = response.Body.Close()
			t.Fatalf("missing cancellation observation endpoint: status %d", response.StatusCode)
		}
		var status struct {
			Started   bool `json:"started"`
			Completed bool `json:"completed"`
			Observed  bool `json:"observed"`
		}
		decodeErr := json.NewDecoder(response.Body).Decode(&status)
		_ = response.Body.Close()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if status.Started {
			cancel()
		}
		if status.Completed {
			if !status.Observed {
				t.Fatal("disconnected net/http request was not observed")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancellation fixture did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("client did not join")
	}
}
