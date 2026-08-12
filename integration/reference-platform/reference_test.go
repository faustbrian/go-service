package referenceplatform_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service"
	referenceplatform "github.com/faustbrian/golib/pkg/service/integration/reference-platform"
)

func TestReferencePlatformRuntimeAndDependencyContract(t *testing.T) {
	t.Parallel()

	dependency := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(dependency.Close)
	business := listen(t)
	management := listen(t)
	definition, err := referenceplatform.New(referenceplatform.Config{
		BusinessListener: business, ManagementListener: management,
		DependencyURL: dependency.URL, Client: dependency.Client(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan int, 1)
	go func() {
		result <- service.Execute(ctx, definition, service.Invocation{
			Args: []string{"serve"}, Stdout: io.Discard, Stderr: io.Discard,
		})
	}()

	managementURL := "http://" + management.Addr().String()
	awaitStatus(t, managementURL+"/startupz", http.StatusOK)
	awaitStatus(t, managementURL+"/readyz", http.StatusOK)
	businessURL := "http://" + business.Addr().String()
	awaitStatus(t, businessURL+"/dependencyz", http.StatusNoContent)
	response, err := http.Get(businessURL + "/runtimez")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	var report referenceplatform.RuntimeReport
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.GOOS != runtime.GOOS || report.GOARCH != runtime.GOARCH || !report.TemporaryStorage {
		t.Fatalf("runtime report = %+v", report)
	}
	resources, err := http.Get(businessURL + "/resourcesz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resources.Body.Close() }()
	var resourceReport referenceplatform.ResourceReport
	if err := json.NewDecoder(resources.Body).Decode(&resourceReport); err != nil {
		t.Fatal(err)
	}
	if resourceReport.HeapSysBytes == 0 || resourceReport.Goroutines == 0 {
		t.Fatalf("resource report = %+v", resourceReport)
	}
	if runtime.GOOS == "linux" && resourceReport.OpenFileDescriptors < 1 {
		t.Fatalf("resource report descriptors = %d", resourceReport.OpenFileDescriptors)
	}

	cancel()
	select {
	case code := <-result:
		if code != 0 {
			t.Fatalf("Execute() exit = %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reference platform did not stop")
	}
}

func TestReferencePlatformRejectsInvalidConfigAndDependencyFailure(t *testing.T) {
	t.Parallel()

	if _, err := referenceplatform.New(referenceplatform.Config{}); err == nil {
		t.Fatal("New(zero) error = nil")
	}
	business := listen(t)
	management := listen(t)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13}}}
	definition, err := referenceplatform.New(referenceplatform.Config{
		BusinessListener: business, ManagementListener: management,
		DependencyURL: "https://127.0.0.1:1", Client: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_ = service.Execute(ctx, definition, service.Invocation{
			Args: []string{"serve"}, Stdout: io.Discard, Stderr: io.Discard,
		})
	}()
	awaitStatus(t, "http://"+management.Addr().String()+"/readyz", http.StatusServiceUnavailable)
	awaitStatus(t, "http://"+business.Addr().String()+"/dependencyz", http.StatusServiceUnavailable)
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func awaitStatus(t *testing.T, endpoint string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(endpoint)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("GET %s did not return %d", endpoint, want)
}
