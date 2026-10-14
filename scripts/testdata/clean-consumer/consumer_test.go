package consumer_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/faustbrian/go-service/v2"
	"github.com/faustbrian/go-service/v2/healthhttp"
	"github.com/faustbrian/go-service/v2/integration"
	"github.com/faustbrian/go-service/v2/serverhttp"
	"github.com/faustbrian/go-service/v2/servicetest"
)

func TestDocumentedPublicPackagesResolve(t *testing.T) {
	component, err := integration.New("consumer", integration.Hooks{})
	if err != nil {
		t.Fatalf("integration.New() error = %v", err)
	}
	runtime, err := service.New(service.Config{
		Components: []service.Component{component},
	})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	if runtime.State() != service.StateNew {
		t.Fatalf("initial state = %v, want %v", runtime.State(), service.StateNew)
	}
	probes, err := healthhttp.New(healthhttp.Config{Lifecycle: runtime})
	if err != nil {
		t.Fatalf("healthhttp.New() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	probes.Liveness().ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/livez", nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want %d", recorder.Code, http.StatusOK)
	}
	fixture, err := servicetest.NewComponent(servicetest.ComponentConfig{
		Name: "fixture",
	})
	if err != nil || fixture.Name != "fixture" {
		t.Fatalf("servicetest.NewComponent() = (%q, %v)", fixture.Name, err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	server, err := serverhttp.New(listener, http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	// Bound the failure control so a missing ownership close cannot hang.
	_ = listener.(*net.TCPListener).SetDeadline(time.Now().Add(100 * time.Millisecond))
	connection, err := listener.Accept()
	if connection != nil {
		_ = connection.Close()
	}
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Close before Run did not release its listener: %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
