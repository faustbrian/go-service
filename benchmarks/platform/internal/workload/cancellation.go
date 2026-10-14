package workload

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// CancellationWait bounds native-runtime observation when disconnect does not
// cancel a request context. It is fixture cleanup, not injected cancellation.
const CancellationWait = 200 * time.Millisecond

// CancellationStatus is one request's independently observed native-context
// result. Unix timestamps share the process harness host clock; no cross-host
// clock comparison is supported.
type CancellationStatus struct {
	// Started acknowledges that the handler is waiting for native cancellation.
	Started bool `json:"started"`
	// Completed reports native observation or bounded timer cleanup completion.
	Completed bool `json:"completed"`
	// Observed reports native request-context cancellation, not timer completion.
	Observed bool `json:"observed"`
	// ObservedUnixNano is the host-clock instant of native cancellation; zero means unobserved.
	ObservedUnixNano int64 `json:"observed_unix_nano"`
	// WaitMilliseconds is handler-start-to-observation or bounded cleanup time.
	WaitMilliseconds float64 `json:"wait_milliseconds"`
}

// CancellationProbe owns two bounded observation slots for sequential
// disconnect and deadline requests. Its mutex protects request/status access;
// callers own listeners and join requests before discarding the probe.
type CancellationProbe struct {
	mu         sync.Mutex
	disconnect CancellationStatus
	deadline   CancellationStatus
}

// Wait observes the supplied native request context without replacing it.
// Unsupported cancellation completes through the separate bounded timer and
// records Observed=false; the timer is always stopped.
func (probe *CancellationProbe) Wait(ctx context.Context, mode string) bool {
	if mode != "disconnect" && mode != "deadline" {
		return false
	}
	probe.set(mode, CancellationStatus{Started: true})
	started := time.Now()
	timer := time.NewTimer(CancellationWait)
	defer timer.Stop()
	status := CancellationStatus{Started: true, Completed: true}
	select {
	case <-ctx.Done():
		status.Observed = true
		status.ObservedUnixNano = time.Now().UnixNano()
	case <-timer.C:
	}
	status.WaitMilliseconds = float64(time.Since(started)) / float64(time.Millisecond)
	probe.set(mode, status)
	return true
}

// Status snapshots the requested bounded observation slot. Unknown modes do
// not allocate slots or mutate the probe.
func (probe *CancellationProbe) Status(mode string) (CancellationStatus, bool) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	switch mode {
	case "disconnect":
		return probe.disconnect, true
	case "deadline":
		return probe.deadline, true
	default:
		return CancellationStatus{}, false
	}
}

func (probe *CancellationProbe) set(mode string, status CancellationStatus) {
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if mode == "disconnect" {
		probe.disconnect = status
	} else {
		probe.deadline = status
	}
}

// Handler runs one native-context observation. It writes only after joining
// observed cancellation or the cleanup timer; disconnect normally prevents the
// client from receiving this response.
func (probe *CancellationProbe) Handler(writer http.ResponseWriter, request *http.Request) {
	if !probe.Wait(request.Context(), request.URL.Query().Get("mode")) {
		http.Error(writer, "unknown cancellation mode", http.StatusBadRequest)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// StatusHandler exposes fixture observations separately from the cancelled
// request so the process runner can measure server-side responsiveness.
func (probe *CancellationProbe) StatusHandler(writer http.ResponseWriter, request *http.Request) {
	status, ok := probe.Status(request.URL.Query().Get("mode"))
	if !ok {
		http.Error(writer, "unknown cancellation mode", http.StatusBadRequest)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(status)
}
