package compatibility_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/faustbrian/go-service/v2"
	"github.com/faustbrian/go-service/v2/serverhttp"
)

func TestGeneratedOpenAPIHandlerCurrentV2ComposesWithServiceHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, generatedContextKey{}, "service-owned")
	endpoint := generatedStatusEndpoint{serve: func(w http.ResponseWriter, r *http.Request) {
		if value, ok := r.Context().Value(generatedContextKey{}).(string); ok {
			w.Header().Set("X-Fixture-Context", value)
		}
		_, _ = io.WriteString(w, "ready\n")
	}}
	runtime, listener, client := startCurrentGeneratedHandlerService(t, ctx, endpoint)
	for _, test := range []struct {
		method, path string
		status       int
		body         string
	}{
		{http.MethodGet, "/generated-status", http.StatusOK, "ready\n"},
		{http.MethodGet, "/not-generated", http.StatusNotFound, "404 page not found\n"},
		{http.MethodPost, "/generated-status", http.StatusMethodNotAllowed, "Method Not Allowed\n"},
	} {
		request, err := http.NewRequestWithContext(ctx, test.method, "http://"+listener.Addr().String()+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || response.StatusCode != test.status || string(body) != test.body {
			t.Fatalf("%s %s: status=%d body=%q read=%v close=%v", test.method, test.path, response.StatusCode, body, readErr, closeErr)
		}
		if test.status == http.StatusOK && response.Header.Get("X-Fixture-Context") != "service-owned" {
			t.Fatal("generated wrapper lost the Service-owned request context")
		}
	}
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	assertCurrentGeneratedHandlerStopped(t, runtime, listener)
}

func TestGeneratedOpenAPIHandlerCurrentV2CancellationJoinsServiceHTTP(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{})
	exited := make(chan struct{})
	canceled := make(chan error, 1)
	endpoint := generatedStatusEndpoint{serve: func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		defer close(exited)
		<-r.Context().Done()
		canceled <- r.Context().Err()
		w.WriteHeader(http.StatusServiceUnavailable)
	}}
	runtime, listener, client := startCurrentGeneratedHandlerService(t, ctx, endpoint)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String()+"/generated-status", nil)
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		defer close(requestDone)
		response, requestErr := client.Do(request)
		if requestErr != nil {
			result <- requestErr
			return
		}
		_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			result <- errors.New("canceled generated handler returned an unexpected status")
			return
		}
		result <- errors.Join(readErr, closeErr)
	}()
	t.Cleanup(func() {
		cancel()
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := runtime.Shutdown(cleanup); err != nil {
			t.Errorf("request cleanup shutdown: %v", err)
		}
		select {
		case <-requestDone:
		case <-cleanup.Done():
			t.Error("generated-handler client request did not join")
		}
	})
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("generated operation was not entered")
	}
	if err := runtime.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	default:
		t.Fatal("Service shutdown returned before the generated handler exited")
	}
	if err := <-canceled; !errors.Is(err, context.Canceled) {
		t.Fatalf("request context cancellation = %v", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("generated-handler response did not complete")
	}
	<-requestDone
	assertCurrentGeneratedHandlerStopped(t, runtime, listener)
}

// startCurrentGeneratedHandlerService transfers a loopback listener to the public
// Service/serverhttp lifetime and retains bounded failure cleanup for the test.
func startCurrentGeneratedHandlerService(t *testing.T, ctx context.Context, endpoint generatedStatusEndpoint) (*service.Service, net.Listener, *http.Client) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	httpServer, err := serverhttp.New(listener, Handler(endpoint), serverhttp.WithShutdownTimeout(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpServer.Close() })
	runtime, err := service.New(service.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := runtime.Shutdown(cleanup); err != nil {
			t.Errorf("Service cleanup: %v", err)
		}
	})
	if err := runtime.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Go("generated-openapi-http", httpServer.Run); err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	return runtime, listener, &http.Client{Transport: transport, Timeout: 2 * time.Second}
}

func assertCurrentGeneratedHandlerStopped(t *testing.T, runtime *service.Service, listener net.Listener) {
	t.Helper()
	if runtime.State() != service.StateStopped {
		t.Fatalf("Service state = %s", runtime.State())
	}
	if err := listener.(*net.TCPListener).SetDeadline(time.Now().Add(100 * time.Millisecond)); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if _, err := listener.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Service-owned listener remains open: %v", err)
	}
}
