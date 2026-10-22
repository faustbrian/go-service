package consumer_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/faustbrian/go-cli/v2"
	"github.com/faustbrian/go-service/v3"
	"github.com/faustbrian/go-service/v3/healthhttp"
	"github.com/faustbrian/go-service/v3/integration"
	"github.com/faustbrian/go-service/v3/serverhttp"
	"github.com/faustbrian/go-service/v3/servicetest"
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
	server, err := serverhttp.New(listener, http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/panic" {
			panic("consumer panic")
		}
		response.WriteHeader(http.StatusOK)
	}), serverhttp.WithPanicRecovery(false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	var panicValue any
	func() {
		defer func() { panicValue = recover() }()
		server.HTTPServer().Handler.ServeHTTP(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "/panic", nil),
		)
	}()
	if panicValue != "consumer panic" {
		t.Fatal("public panic policy did not preserve native handler behavior")
	}
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

func TestTypedCommandInputReachesConfiguration(t *testing.T) {
	t.Parallel()

	address := cli.StringOption("address").Default("127.0.0.1:8080")
	built := false
	command := service.CommandFor(service.CommandSpec[string]{
		Name: "migrate", Kind: service.CommandKindOneShot,
		Options: []cli.OptionDefinition{address},
		Load: func(_ context.Context, invocation service.Invocation) (string, error) {
			input := invocation.Input()
			if address.State(input) != cli.ValueExplicit {
				t.Error("declared input lost explicit provenance")
			}
			return address.Get(input), nil
		},
		Build: func(_ context.Context, _ service.BuildContext, value string) (service.Plan, error) {
			built = true
			if value != ":9000" {
				t.Errorf("configured address = %q", value)
			}
			return service.Plan{}, nil
		},
	})
	exit := service.Execute(context.Background(), service.Definition{
		Identity: service.Identity{Name: "consumer"}, Commands: service.Commands{Migrate: command},
	}, service.Invocation{Args: []string{"migrate", "--address=:9000"}, Stdout: io.Discard, Stderr: io.Discard})
	if exit != 0 || !built {
		t.Fatalf("exit = %d, built = %v", exit, built)
	}
}

// The public module must enforce admission, not merely export the option.
func TestPublicConnectionAdmission(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan struct{}, 3)
	runtime, err := serverhttp.New(listener, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, "public-bounded") }), serverhttp.WithMaxConnections(1), serverhttp.WithShutdownTimeout(time.Second), serverhttp.WithConnContext(func(ctx context.Context, _ net.Conn) context.Context { accepted <- struct{}{}; return ctx }))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	var clients []net.Conn
	t.Cleanup(func() {
		for _, client := range clients {
			_ = client.Close()
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("public server failed to join")
		}
	})
	dial := func() net.Conn {
		t.Helper()
		connection, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, connection)
		return connection
	}
	await := func() {
		t.Helper()
		select {
		case <-accepted:
		case <-time.After(time.Second):
			t.Fatal("public admission failed to progress")
		}
	}
	first := dial()
	await()
	next := dial()
	select {
	case <-accepted:
		t.Fatal("public connection bound was ignored")
	case <-time.After(50 * time.Millisecond):
	}
	_ = first.Close()
	await()
	if err := next.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Close = true
	if err := request.Write(next); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(next), request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || string(body) != "public-bounded" {
		t.Fatal("public admitted response failed")
	}
}
