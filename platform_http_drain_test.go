package service

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/go-service/v3/serverhttp"
)

func TestHTTPHandlerDrainHandlesNeverStartedAndLateAdmission(t *testing.T) {
	t.Parallel()
	drain := newTestHandlerOwner(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("late request entered application")
	}))
	if err := drain.CloseAdmission(); err != nil {
		t.Fatal(err)
	}
	if err := drain.CloseAdmission(); err != nil {
		t.Fatal(err)
	}
	if err := drain.WaitHandlers(t.Context()); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	drain.HTTPServer().Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("late admission = %d", response.Code)
	}
}

func TestHTTPHandlerJoinSurvivesLaterTaskRegistrationFailure(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	entered, finalizing, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	server, err := serverhttp.New(listener, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
	}), serverhttp.WithShutdownTimeout(25*time.Millisecond), serverhttp.WithHandlerJoin(true))
	if err != nil {
		t.Fatal(err)
	}
	// This is the exact internal boundary after the HTTP task was admitted:
	// the later registration is rejected while HTTP finalization still belongs
	// to that already registered task, not to a separately registered join task.
	owner := &businessOwner{server: server, config: HTTP{JoinHandlers: true}, stopped: make(chan struct{})}
	stopped := make(chan struct{})
	runtime, err := New(Config{Components: []Component{
		{Name: "database", Stop: func(context.Context) error { close(stopped); return nil }},
		{Name: "http", CloseAdmission: server.CloseAdmission, Stop: owner.stop},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Go("http", func(ctx context.Context) error {
		err := owner.run(ctx)
		close(finalizing)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		client := &http.Client{Timeout: time.Second}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String(), nil)
		if err != nil {
			t.Error(err)
			return
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()
	_ = receiveTestValue(t, entered)
	if err = runtime.Drain(); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- executeLongRunning(t.Context(), Invocation{Stdout: io.Discard, Stderr: io.Discard}, runtime,
			[]Task{{Name: "later", Run: func(context.Context) error { t.Error("rejected task ran"); return nil }}}, nil, time.Second)
	}()
	_ = receiveTestValue(t, owner.stopped)
	select {
	case <-stopped:
		t.Error("registration failure released dependencies before admitted handler")
	case <-result:
		t.Error("registration failure returned before admitted handler")
	default:
	}
	unblock()
	if err = receiveTestValue(t, result); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("registration failure was lost: %v", err)
	}
	_ = receiveTestValue(t, finalizing)
	_ = receiveTestValue(t, stopped)
	_ = receiveTestValue(t, clientDone)
}

func TestHTTPHandlerDrainReleasesPanickingHandler(t *testing.T) {
	t.Parallel()
	drain := newTestHandlerOwner(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler panic")
	}), serverhttp.WithPanicRecovery(false))
	func() {
		defer func() {
			if recover() != "handler panic" {
				t.Error("handler panic changed")
			}
		}()
		drain.HTTPServer().Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	if err := drain.WaitHandlers(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPHandlerDrainOwnsEveryHTTP2Stream(t *testing.T) {
	type admitted struct {
		protocol int
		peer     string
	}
	entered := make(chan admitted, 2)
	finalizing, returned := make(chan struct{}, 2), make(chan struct{}, 2)
	clients := make(chan struct{}, 2)
	releases := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var next atomic.Int32
	var once [2]sync.Once
	release := func(index int) { once[index].Do(func() { close(releases[index]) }) }
	drain := newTestHandlerOwner(t, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		index := next.Add(1) - 1
		entered <- admitted{protocol: request.ProtoMajor, peer: request.RemoteAddr}
		<-request.Context().Done()
		finalizing <- struct{}{}
		<-releases[index]
		returned <- struct{}{}
	}))
	server := httptest.NewUnstartedServer(drain.HTTPServer().Handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(func() {
		release(0)
		release(1)
		server.Close()
	})
	client := server.Client()
	client.Timeout = 3 * time.Second
	transport := client.Transport.(*http.Transport)
	transport.MaxConnsPerHost = 1
	for range 2 {
		go func() {
			defer func() { clients <- struct{}{} }()
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
			if err != nil {
				t.Error(err)
				return
			}
			response, err := client.Do(request)
			if err == nil {
				_ = response.Body.Close()
			}
		}()
	}
	first, second := receiveTestValue(t, entered), receiveTestValue(t, entered)
	if first.protocol != 2 || second.protocol != 2 || first.peer != second.peer {
		t.Fatalf("fixture did not use two streams on one HTTP/2 connection: %v %v", first, second)
	}
	if err := drain.CloseAdmission(); err != nil {
		t.Fatal(err)
	}
	server.CloseClientConnections()
	_ = receiveTestValue(t, finalizing)
	_ = receiveTestValue(t, finalizing)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := drain.WaitHandlers(ctx); !errors.Is(err, serverhttp.ErrHandlersUnjoined) {
		t.Fatalf("connection closure was mistaken for handler completion: %v", err)
	}
	release(0)
	_ = receiveTestValue(t, returned)
	if err := drain.WaitHandlers(ctx); !errors.Is(err, serverhttp.ErrHandlersUnjoined) {
		t.Fatalf("first stream released ownership of held second stream: %v", err)
	}
	release(1)
	_ = receiveTestValue(t, returned)
	if err := drain.WaitHandlers(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = receiveTestValue(t, clients)
	_ = receiveTestValue(t, clients)
	if err := drain.CloseAdmission(); err != nil {
		t.Fatal(err)
	}
}

func newTestHandlerOwner(t *testing.T, handler http.Handler, options ...serverhttp.Option) *serverhttp.Server {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	options = append(options, serverhttp.WithHandlerJoin(true))
	server, err := serverhttp.New(listener, handler, options...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	return server
}
