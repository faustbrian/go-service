package service_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service"
)

func TestCohesiveMaintenanceCommandsControlAdmissionReadinessAndBypass(t *testing.T) {
	store, err := service.NewFileMaintenanceStore(filepath.Join(t.TempDir(), "maintenance.json"))
	if err != nil {
		t.Fatalf("NewFileMaintenanceStore() error = %v", err)
	}
	business, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("business net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = business.Close() })
	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })

	started := make(chan struct{})
	serve := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "serve",
		Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{
				HTTP: &service.HTTP{
					Listener: business,
					Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
						_, _ = io.WriteString(writer, "application")
					}),
				},
				Tasks: []service.Task{{
					Name: "application-loop",
					Run: func(ctx context.Context) error {
						close(started)
						<-ctx.Done()
						return context.Cause(ctx)
					},
				}},
			}, nil
		},
	})
	definition := service.Definition{
		Identity:   service.Identity{Name: "postal"},
		Commands:   service.Commands{Serve: serve},
		Management: service.Management{Listener: management},
		Maintenance: service.Maintenance{
			Store: store, RefreshInterval: 10 * time.Millisecond,
			OperationTimeout: time.Second,
		},
	}

	var downOutput bytes.Buffer
	if exit := service.Execute(context.Background(), definition, service.Invocation{
		Args: []string{
			"down", "--retry=1m", "--refresh=15s",
			"--secret=maintenance-token", "--redirect=/maintenance",
		},
		Stdout: &downOutput, Stderr: io.Discard,
	}); exit != 0 {
		t.Fatalf("down exit = %d", exit)
	}
	if !strings.Contains(downOutput.String(), "maintenance enabled") {
		t.Fatalf("down output = %q", downOutput.String())
	}

	signals := make(chan os.Signal, 1)
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), definition, service.Invocation{
			Args: []string{"serve"}, Signals: signals,
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
	case <-time.After(5 * time.Second):
		t.Fatal("service did not start")
	}

	client := &http.Client{
		Timeout: time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	businessURL := "http://" + business.Addr().String()
	response := maintenanceRequest(t, client, businessURL+"/business", nil)
	if response.StatusCode != http.StatusFound || response.Header.Get("Location") != "/maintenance" ||
		response.Header.Get("Retry-After") != "60" || response.Header.Get("Refresh") != "15" {
		t.Fatalf("maintenance response = %d, headers = %#v", response.StatusCode, response.Header)
	}
	_ = response.Body.Close()
	redirectTarget := maintenanceRequest(t, client, businessURL+"/maintenance", nil)
	_ = redirectTarget.Body.Close()
	if redirectTarget.StatusCode != http.StatusOK {
		t.Fatalf("maintenance redirect target status = %d, want 200", redirectTarget.StatusCode)
	}

	bypass := maintenanceRequest(t, client, businessURL+"/maintenance-token", nil)
	if bypass.StatusCode != http.StatusFound || len(bypass.Cookies()) != 1 {
		t.Fatalf("bypass response = %d, cookies = %#v", bypass.StatusCode, bypass.Cookies())
	}
	cookie := bypass.Cookies()[0]
	_ = bypass.Body.Close()
	allowed := maintenanceRequest(t, client, businessURL+"/business", cookie)
	body, err := io.ReadAll(allowed.Body)
	if err != nil {
		t.Fatalf("read allowed response: %v", err)
	}
	_ = allowed.Body.Close()
	if allowed.StatusCode != http.StatusOK || string(body) != "application" {
		t.Fatalf("bypassed response = %d %q", allowed.StatusCode, body)
	}

	managementURL := "http://" + management.Addr().String() + "/readyz"
	if status := probeStatus(t, managementURL); status != http.StatusServiceUnavailable {
		t.Fatalf("maintenance readiness = %d, want 503", status)
	}

	var statusOutput bytes.Buffer
	if exit := service.Execute(context.Background(), definition, service.Invocation{
		Args: []string{"status"}, Stdout: &statusOutput, Stderr: io.Discard,
	}); exit != 0 || !strings.Contains(statusOutput.String(), `"enabled":true`) {
		t.Fatalf("status exit/output = %d/%q", exit, statusOutput.String())
	}
	if exit := service.Execute(context.Background(), definition, service.Invocation{
		Args: []string{"up"}, Stdout: io.Discard, Stderr: io.Discard,
	}); exit != 0 {
		t.Fatalf("up exit = %d", exit)
	}

	deadline := time.Now().Add(time.Second)
	for probeStatus(t, managementURL) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("readiness did not recover after maintenance ended")
		}
		time.Sleep(time.Millisecond)
	}
	response = maintenanceRequest(t, client, businessURL+"/business", nil)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("post-maintenance response = %d, want 200", response.StatusCode)
	}

	signals <- os.Interrupt
	select {
	case exit := <-result:
		if exit != 130 {
			t.Fatalf("serve exit = %d, want 130", exit)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not stop")
	}
}

func TestMaintenanceCustomResponseControlsHeadersWhilePlatformForces503(t *testing.T) {
	store, err := service.NewSharedMaintenanceStore(service.MaintenanceStoreOperations{
		Load: func(context.Context) (service.MaintenanceState, error) {
			return service.MaintenanceState{Enabled: true, Since: time.Now()}, nil
		},
		Store: func(context.Context, service.MaintenanceState) error { return nil },
		Clear: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("NewSharedMaintenanceStore() error = %v", err)
	}
	business, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("business net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = business.Close() })
	management, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("management net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = management.Close() })
	serve := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "serve", Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) {
			return struct{}{}, nil
		},
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{HTTP: &service.HTTP{
				Listener: business,
				Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(writer, "application")
				}),
			}}, nil
		},
	})
	var logOutput bytes.Buffer
	recorder := &runtimeEventRecorder{}
	signals := make(chan os.Signal, 1)
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(context.Background(), service.Definition{
			Identity:   service.Identity{Name: "postal"},
			Commands:   service.Commands{Serve: serve},
			Logger:     slog.New(slog.NewJSONHandler(&logOutput, nil)),
			Observer:   recorder,
			Management: service.Management{Listener: management},
			Maintenance: service.Maintenance{
				Store: store, RefreshInterval: 10 * time.Millisecond,
				Response: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
					writer.Header().Set("Content-Type", "text/html; charset=utf-8")
					writer.WriteHeader(http.StatusTeapot)
					_, _ = io.WriteString(writer, "<h1>maintenance</h1>")
				}),
			},
		}, service.Invocation{
			Args: []string{"serve"}, Signals: signals,
			Stdout: io.Discard, Stderr: io.Discard,
		})
	}()
	t.Cleanup(func() {
		select {
		case signals <- os.Interrupt:
		default:
		}
	})

	client := &http.Client{Timeout: time.Second}
	url := "http://" + business.Addr().String() + "/"
	var response *http.Response
	deadline := time.Now().Add(time.Second)
	for {
		response, err = client.Get(url)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("business listener did not start: %v", err)
		}
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read custom response: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable ||
		response.Header.Get("Content-Type") != "text/html; charset=utf-8" ||
		string(body) != "<h1>maintenance</h1>" {
		t.Fatalf("custom maintenance response = %d %q %q", response.StatusCode, response.Header.Get("Content-Type"), body)
	}
	requestObserved := false
	for _, event := range recorder.snapshot() {
		if event.Kind == service.RuntimeEventRequest && event.Method == http.MethodGet &&
			event.Status == http.StatusServiceUnavailable {
			requestObserved = true
			break
		}
	}
	if !requestObserved {
		t.Fatal("maintenance request was not reported through runtime observability")
	}
	requestLogged := false
	for _, record := range decodeJSONLogs(t, logOutput.Bytes()) {
		if record["event.kind"] == string(service.RuntimeEventRequest) &&
			record["http.request.method"] == http.MethodGet &&
			record["http.response.status_code"] == float64(http.StatusServiceUnavailable) &&
			record["correlation.id"] == "[redacted]" && record["request.id"] == "[redacted]" {
			requestLogged = true
			break
		}
	}
	if !requestLogged {
		t.Fatal("maintenance request log lacks bounded HTTP and correlation metadata")
	}

	request, err := http.NewRequest(http.MethodHead, url, nil)
	if err != nil {
		t.Fatalf("HEAD request: %v", err)
	}
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("HEAD maintenance request: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read HEAD response: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable || len(body) != 0 {
		t.Fatalf("HEAD maintenance response = %d %q", response.StatusCode, body)
	}

	signals <- os.Interrupt
	select {
	case <-result:
	case <-time.After(time.Second):
		t.Fatal("serve did not stop")
	}
}

func maintenanceRequest(
	t *testing.T,
	client *http.Client,
	url string,
	cookie *http.Cookie,
) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("maintenance request error = %v", err)
	}
	return response
}
