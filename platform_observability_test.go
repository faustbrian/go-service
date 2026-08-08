package service_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service"
)

type runtimeEventRecorder struct {
	mu     sync.Mutex
	events []service.RuntimeEvent
}

func (recorder *runtimeEventRecorder) ObserveRuntime(
	_ context.Context,
	event service.RuntimeEvent,
) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, event)
}

func (recorder *runtimeEventRecorder) snapshot() []service.RuntimeEvent {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]service.RuntimeEvent(nil), recorder.events...)
}

func TestCohesivePlatformEnrichesLogsAndReportsRuntimeBoundaries(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	recorder := &runtimeEventRecorder{}
	var buildLogger *slog.Logger
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "migrate",
		Kind: service.CommandKindOneShot,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(
			_ context.Context,
			build service.BuildContext,
			_ struct{},
		) (service.Plan, error) {
			buildLogger = build.Logger

			return service.Plan{
				Components: []service.Component{{
					Name:  "database",
					Start: func(context.Context) error { return nil },
					Stop:  func(context.Context) error { return nil },
				}},
				Tasks: []service.Task{{
					Name: "schema",
					Run:  func(context.Context) error { return nil },
				}},
			}, nil
		},
	})
	exit := service.Execute(context.Background(), service.Definition{
		Identity: service.Identity{
			Name: "postal", Version: "1.2.3", Commit: "abcdef12",
			Environment: "production", Instance: "postal-1",
		},
		Commands:              service.Commands{Migrate: command},
		Logger:                logger,
		Observer:              recorder,
		CorrelationDisclosure: correlation.DisclosurePolicy{Mode: correlation.ExposeDisclosure},
	}, service.Invocation{
		Args: []string{"migrate"}, Stdout: io.Discard, Stderr: io.Discard,
	})
	if exit != 0 {
		t.Fatalf("Execute() exit = %d, want 0", exit)
	}
	if buildLogger == nil || buildLogger == logger {
		t.Fatal("BuildContext.Logger was not the platform-enriched logger")
	}

	logs := decodeJSONLogs(t, output.Bytes())
	if len(logs) == 0 {
		t.Fatal("platform emitted no lifecycle logs")
	}
	for _, record := range logs {
		if record["service.name"] != "postal" || record["service.version"] != "1.2.3" ||
			record["process.role"] != "migrate" || record["deployment.environment"] != "production" ||
			record["service.instance.id"] != "postal-1" {
			t.Fatalf("unenriched log record = %#v", record)
		}
		if record["correlation.id"] == "" || record["request.id"] == "" {
			t.Fatalf("log lacks correlation identity = %#v", record)
		}
	}

	events := recorder.snapshot()
	assertRuntimeEvent(t, events, service.RuntimeEventStartup, service.RuntimeResultStarted, "")
	assertRuntimeEvent(t, events, service.RuntimeEventConstruction, service.RuntimeResultStarted, "migrate")
	assertRuntimeEvent(t, events, service.RuntimeEventConstruction, service.RuntimeResultSucceeded, "migrate")
	assertRuntimeEvent(t, events, service.RuntimeEventStartup, service.RuntimeResultSucceeded, "")
	assertRuntimeEvent(t, events, service.RuntimeEventReadiness, service.RuntimeResultAvailable, "")
	assertRuntimeEvent(t, events, service.RuntimeEventTask, service.RuntimeResultStarted, "schema")
	assertRuntimeEvent(t, events, service.RuntimeEventTask, service.RuntimeResultSucceeded, "schema")
	assertRuntimeEvent(t, events, service.RuntimeEventComponentStart, service.RuntimeResultStarted, "database")
	assertRuntimeEvent(t, events, service.RuntimeEventComponentStart, service.RuntimeResultSucceeded, "database")
	assertRuntimeEvent(t, events, service.RuntimeEventComponentStop, service.RuntimeResultStarted, "database")
	assertRuntimeEvent(t, events, service.RuntimeEventComponentStop, service.RuntimeResultSucceeded, "database")
	assertRuntimeEvent(t, events, service.RuntimeEventDrain, service.RuntimeResultStarted, "")
	assertRuntimeEvent(t, events, service.RuntimeEventShutdown, service.RuntimeResultSucceeded, "")
	for _, event := range events {
		if event.Identity.Name != "postal" || event.Identity.Role != "migrate" || event.At.IsZero() {
			t.Fatalf("incomplete runtime event = %#v", event)
		}
	}
}

func TestCohesivePlatformReportsPanickingSupervisedTask(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	recorder := &runtimeEventRecorder{}
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{Tasks: []service.Task{{
				Name: "worker-loop",
				Run:  func(context.Context) error { panic("worker panic") },
			}}}, nil
		},
	})
	exit := service.Execute(context.Background(), service.Definition{
		Identity:   service.Identity{Name: "postal"},
		Commands:   service.Commands{Worker: command},
		Observer:   recorder,
		Management: service.Management{Listener: listener},
	}, service.Invocation{
		Args: []string{"worker"}, Stdout: io.Discard, Stderr: io.Discard,
	})
	if exit == 0 {
		t.Fatal("Execute() hid a panicking supervised task")
	}
	assertRuntimeEvent(
		t, recorder.snapshot(), service.RuntimeEventTask, service.RuntimeResultFailed, "worker-loop",
	)
}

func TestManagementProbesReportResultsWithoutLoggingSuccessfulRequests(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	var output bytes.Buffer
	recorder := &runtimeEventRecorder{}
	var dependencyReady atomic.Bool
	dependencyReady.Store(true)
	started := make(chan struct{})
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "worker",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{
				Tasks: []service.Task{{
					Name: "worker-loop",
					Run: func(ctx context.Context) error {
						close(started)
						<-ctx.Done()
						return context.Cause(ctx)
					},
				}},
				Readiness: []service.ReadinessCheck{{
					Name: "database",
					Run: func(context.Context) error {
						if !dependencyReady.Load() {
							return context.DeadlineExceeded
						}
						return nil
					},
				}},
			}, nil
		},
	})
	signals := make(chan os.Signal, 1)
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity:   service.Identity{Name: "postal"},
			Commands:   service.Commands{Worker: command},
			Logger:     slog.New(slog.NewJSONHandler(&output, nil)),
			Observer:   recorder,
			Management: service.Management{Listener: listener},
		}, service.Invocation{
			Args: []string{"worker"}, Signals: signals,
			Stdout: io.Discard, Stderr: io.Discard,
		})
	}()
	t.Cleanup(func() {
		select {
		case signals <- os.Interrupt:
		default:
		}
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}

	url := "http://" + listener.Addr().String() + "/readyz"
	if status := probeStatus(t, url); status != http.StatusOK {
		t.Fatalf("initial readiness status = %d, want 200", status)
	}
	dependencyReady.Store(false)
	if status := probeStatus(t, url); status != http.StatusServiceUnavailable {
		t.Fatalf("failed readiness status = %d, want 503", status)
	}
	if status := probeStatus(t, url); status != http.StatusServiceUnavailable {
		t.Fatalf("repeated readiness status = %d, want 503", status)
	}
	dependencyReady.Store(true)
	if status := probeStatus(t, url); status != http.StatusOK {
		t.Fatalf("recovered readiness status = %d, want 200", status)
	}

	events := recorder.snapshot()
	probeEvents := 0
	transitions := 0
	for _, event := range events {
		if event.Kind != service.RuntimeEventProbe || event.Boundary != "readiness" {
			continue
		}
		probeEvents++
		if event.Transition {
			transitions++
		}
	}
	if probeEvents != 4 || transitions != 3 {
		t.Fatalf("probe events = %d, transitions = %d; want 4 and 3", probeEvents, transitions)
	}

	probeLogs := 0
	for _, record := range decodeJSONLogs(t, output.Bytes()) {
		if record["event.kind"] == string(service.RuntimeEventProbe) {
			probeLogs++
		}
	}
	if probeLogs != 3 {
		t.Fatalf("probe transition logs = %d, want 3", probeLogs)
	}

	signals <- os.Interrupt
	select {
	case exit := <-result:
		if exit != 130 {
			t.Fatalf("Execute() exit = %d, want 130", exit)
		}
	case <-time.After(time.Second):
		t.Fatal("Execute() did not stop")
	}
	assertRuntimeEvent(
		t, recorder.snapshot(), service.RuntimeEventTask, service.RuntimeResultSucceeded, "worker-loop",
	)
}

func probeStatus(t *testing.T, url string) int {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close probe response: %v", err)
	}
	return response.StatusCode
}

func decodeJSONLogs(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var records []map[string]any
	for decoder.More() {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			t.Fatalf("decode log: %v", err)
		}
		records = append(records, record)
	}
	return records
}

func assertRuntimeEvent(
	t *testing.T,
	events []service.RuntimeEvent,
	kind service.RuntimeEventKind,
	result service.RuntimeEventResult,
	boundary string,
) {
	t.Helper()
	for _, event := range events {
		if event.Kind == kind && event.Result == result && event.Boundary == boundary {
			return
		}
	}
	t.Fatalf("missing runtime event (%q, %q, %q) in %#v", kind, result, boundary, events)
}
