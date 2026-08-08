package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/correlation"
)

func TestRuntimeObservabilityDistinguishesDisabledAndBoundedLogResults(t *testing.T) {
	identity := ProcessIdentity{Identity: Identity{Name: "postal"}, Role: "serve"}
	if observability := newRuntimeObservability(
		t.Context(), identity, nil, nil, correlation.DisclosurePolicy{},
	); observability != nil {
		t.Fatal("observability enabled without a logger or observer")
	}
	observerOnly := newRuntimeObservability(
		t.Context(), identity, nil,
		RuntimeObserverFunc(func(context.Context, RuntimeEvent) {}),
		correlation.DisclosurePolicy{},
	)
	if observerOnly == nil {
		t.Fatal("observer-only observability was disabled")
	}

	values := correlation.Values{
		CorrelationID: correlation.MustCorrelationID("correlation", correlation.Policy{}),
		RequestID:     correlation.MustRequestID("request", correlation.Policy{}),
	}
	var output bytes.Buffer
	loggerOnly := newRuntimeObservability(
		correlation.WithValues(t.Context(), values), identity,
		slog.New(slog.NewJSONHandler(&output, nil)), nil,
		correlation.DisclosurePolicy{Mode: correlation.ExposeDisclosure},
	)
	if loggerOnly == nil {
		t.Fatal("logger-only observability was disabled")
	}
	loggerOnly.logger.InfoContext(t.Context(), "build log")
	loggerOnly.event(t.Context(), RuntimeEventComponentStart, RuntimeResultStarted, "database", time.Nanosecond, false)
	loggerOnly.event(t.Context(), RuntimeEventStartup, RuntimeResultSucceeded, "", 0, true)
	loggerOnly.event(t.Context(), RuntimeEventStartup, RuntimeResultFailed, "", 0, false)
	loggerOnly.event(t.Context(), RuntimeEventProbe, RuntimeResultUnavailable, "readiness", 0, false)
	records := decodeObservabilityLogs(t, output.Bytes())
	if records[0]["correlation.id"] != "correlation" || records[0]["request.id"] != "request" {
		t.Fatalf("build logger identity = %#v", records[0])
	}
	if records[1]["event.boundary"] != "database" || records[1]["event.duration"] == nil ||
		records[1]["level"] != "INFO" {
		t.Fatalf("bounded component log = %#v", records[1])
	}
	if records[2]["event.boundary"] != nil || records[2]["event.duration"] != nil ||
		records[2]["event.transition"] != true || records[2]["level"] != "INFO" {
		t.Fatalf("zero-duration transition log = %#v", records[2])
	}
	if records[3]["level"] != "ERROR" || records[4]["level"] != "ERROR" {
		t.Fatalf("failure levels = %#v %#v", records[3], records[4])
	}
}

func TestRequestResultClassificationCoversStatusAndPanicBoundaries(t *testing.T) {
	var output bytes.Buffer
	var events []RuntimeEvent
	observability := newRuntimeObservability(
		t.Context(), ProcessIdentity{Identity: Identity{Name: "postal"}, Role: "serve"},
		slog.New(slog.NewJSONHandler(&output, nil)),
		RuntimeObserverFunc(func(_ context.Context, event RuntimeEvent) { events = append(events, event) }),
		correlation.DisclosurePolicy{},
	)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, result := range []struct {
		status   int
		panicked bool
		want     RuntimeEventResult
	}{
		{status: http.StatusInternalServerError - 1, want: RuntimeResultSucceeded},
		{status: http.StatusInternalServerError, want: RuntimeResultFailed},
		{status: http.StatusOK, panicked: true, want: RuntimeResultFailed},
	} {
		observability.finishRequest(request, result.status, time.Now(), result.panicked)
		if got := events[len(events)-1].Result; got != result.want {
			t.Fatalf("status %d panicked %v result = %q", result.status, result.panicked, got)
		}
	}
	records := decodeObservabilityLogs(t, output.Bytes())
	if records[0]["level"] != "INFO" || records[1]["level"] != "ERROR" ||
		records[2]["level"] != "ERROR" {
		t.Fatalf("request levels = %#v", records)
	}
}

func decodeObservabilityLogs(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var records []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(data), []byte("\n")) {
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			t.Fatalf("decode log: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func TestObservedComponentOperationsReportFailuresAndPanics(t *testing.T) {
	var events []RuntimeEvent
	observability := newRuntimeObservability(
		t.Context(),
		ProcessIdentity{Identity: Identity{Name: "postal"}, Role: "serve"},
		nil,
		RuntimeObserverFunc(func(_ context.Context, event RuntimeEvent) {
			events = append(events, event)
		}),
		correlation.DisclosurePolicy{},
	)
	wantErr := errors.New("start failed")
	failing := observeComponentOperation(
		"database", RuntimeEventComponentStart,
		func(context.Context) error { return wantErr }, observability,
	)
	if err := failing(t.Context()); !errors.Is(err, wantErr) {
		t.Fatalf("failing operation error = %v", err)
	}
	if len(events) != 2 || events[1].Result != RuntimeResultFailed {
		t.Fatalf("failure events = %#v", events)
	}

	events = nil
	panicking := observeComponentOperation(
		"database", RuntimeEventComponentStop,
		func(context.Context) error { panic("component panic") }, observability,
	)
	defer func() {
		if value := recover(); value != "component panic" {
			t.Fatalf("panic = %#v", value)
		}
		if len(events) != 2 || events[1].Result != RuntimeResultFailed {
			t.Fatalf("panic events = %#v", events)
		}
	}()
	_ = panicking(t.Context())
}

func TestRuntimeObservabilityContainsObserverPanicsAndWriterEdges(t *testing.T) {
	var output bytes.Buffer
	panicking := RuntimeObserverFunc(func(context.Context, RuntimeEvent) {
		panic("secret panic value")
	})
	observability := newRuntimeObservability(
		t.Context(),
		ProcessIdentity{Identity: Identity{Name: "postal"}, Role: "serve"},
		slog.New(slog.NewJSONHandler(&output, nil)),
		panicking,
		correlation.DisclosurePolicy{},
	)
	observability.event(t.Context(), RuntimeEventStartup, RuntimeResultStarted, "", 0, false)
	if !strings.Contains(output.String(), "observer-panic") || strings.Contains(output.String(), "secret panic value") {
		t.Fatalf("observer panic log = %q", output.String())
	}

	if logger := observability.loggerForContext(context.Background()); logger == nil {
		t.Fatal("loggerForContext() lost identity logger without correlation")
	}
	values := correlation.Values{
		CorrelationID: correlation.MustCorrelationID("correlation", correlation.Policy{}),
		RequestID:     correlation.MustRequestID("request", correlation.Policy{}),
	}
	invalidDisclosure := newRuntimeObservability(
		correlation.WithValues(t.Context(), values),
		ProcessIdentity{Identity: Identity{Name: "postal"}, Role: "serve"},
		slog.New(slog.NewTextHandler(&output, nil)),
		panicking,
		correlation.DisclosurePolicy{Mode: 99},
	)
	if logger := invalidDisclosure.loggerForContext(
		correlation.WithValues(t.Context(), values),
	); logger == nil {
		t.Fatal("invalid disclosure removed the safe identity logger")
	}

	recorder := httptest.NewRecorder()
	writer := &observedResponseWriter{ResponseWriter: recorder, status: http.StatusOK}
	if writer.Unwrap() != recorder {
		t.Fatal("Unwrap() did not preserve the underlying writer")
	}
	if _, err := writer.Write([]byte("ok")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	writer.WriteHeader(http.StatusTeapot)
	if recorder.Code != http.StatusOK {
		t.Fatalf("duplicate WriteHeader changed status to %d", recorder.Code)
	}
	if boundedHTTPMethod("CUSTOM") != "OTHER" {
		t.Fatal("custom HTTP method was not bounded")
	}
}

func TestRequestObservationReportsPanicsBeforeRecovery(t *testing.T) {
	var events []RuntimeEvent
	observability := newRuntimeObservability(
		t.Context(),
		ProcessIdentity{Identity: Identity{Name: "postal"}, Role: "serve"},
		nil,
		RuntimeObserverFunc(func(_ context.Context, event RuntimeEvent) {
			events = append(events, event)
		}),
		correlation.DisclosurePolicy{},
	)
	handler := observability.request(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler panic")
	}))
	defer func() {
		if value := recover(); value != "handler panic" {
			t.Fatalf("panic = %#v", value)
		}
		if len(events) != 1 || events[0].Kind != RuntimeEventRequest ||
			events[0].Result != RuntimeResultFailed ||
			events[0].Status != http.StatusInternalServerError {
			t.Fatalf("panic request events = %#v", events)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
