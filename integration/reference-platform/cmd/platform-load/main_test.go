package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunMeasuresBoundedEquivalentRequests(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/":
			requests.Add(1)
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = writer.Write([]byte("reference-platform\n"))
		case "/resourcesz":
			_ = json.NewEncoder(writer).Encode(map[string]int64{
				"heap_alloc_bytes":      1 << 20,
				"heap_sys_bytes":        2 << 20,
				"goroutines":            12,
				"open_file_descriptors": 24,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	report, err := run(context.Background(), config{
		Endpoint: server.URL + "/", ResourcesEndpoint: server.URL + "/resourcesz",
		Requests: 200, Concurrency: 8, RequestTimeout: time.Second,
		SampleInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if report.Completed != 200 || report.Failed != 0 || requests.Load() != 200 {
		t.Fatalf("run() report = %+v, requests = %d", report, requests.Load())
	}
	if report.MaxInFlight < 1 || report.MaxInFlight > 8 || report.RequestsPerSecond <= 0 {
		t.Fatalf("run() concurrency report = %+v", report)
	}
	if report.P50 <= 0 || report.P95 < report.P50 || report.P99 < report.P95 {
		t.Fatalf("run() latency report = %+v", report)
	}
	if report.MaxHeapAllocBytes < 1<<20 || report.MaxHeapSysBytes < 2<<20 ||
		report.MaxGoroutines < 12 || report.MaxOpenFileDescriptors < 24 {
		t.Fatalf("run() resource report = %+v", report)
	}
}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	_, err := run(context.Background(), config{})
	if err == nil {
		t.Fatal("run(zero) error = nil")
	}
}
