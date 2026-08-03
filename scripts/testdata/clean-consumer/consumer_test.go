package consumer_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/faustbrian/golib/pkg/service"
	"github.com/faustbrian/golib/pkg/service/healthhttp"
	"github.com/faustbrian/golib/pkg/service/integration"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
	"github.com/faustbrian/golib/pkg/service/servicetest"
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
	var constructor func(net.Listener, http.Handler, ...serverhttp.Option) (*serverhttp.Server, error)
	constructor = serverhttp.New
	if constructor == nil {
		t.Fatal("serverhttp.New is nil")
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
