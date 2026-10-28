package serverhttp_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/faustbrian/go-service/v3/serverhttp"
)

func TestHandlerJoinOwnsNativeHTTP2StreamsAfterClose(t *testing.T) {
	certificate := httptest.NewTLSServer(http.NotFoundHandler())
	config := certificate.TLS.Clone()
	config.NextProtos = []string{"h2", "http/1.1"}
	roots := x509.NewCertPool()
	roots.AddCert(certificate.Certificate())
	certificate.Close()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	type admission struct {
		protocol int
		peer     string
	}
	entered := make(chan admission, 2)
	finalizing, returned, clients := make(chan struct{}, 2), make(chan struct{}, 2), make(chan struct{}, 2)
	var next atomic.Int32
	releases := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var once [2]sync.Once
	release := func(index int) { once[index].Do(func() { close(releases[index]) }) }
	runtime, err := serverhttp.New(tls.NewListener(listener, config), http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		index := next.Add(1) - 1
		entered <- admission{request.ProtoMajor, request.RemoteAddr}
		<-request.Context().Done()
		finalizing <- struct{}{}
		<-releases[index]
		returned <- struct{}{}
	}), serverhttp.WithHandlerJoin(true))
	if err != nil {
		t.Fatal(err)
	}
	runtime.HTTPServer().TLSConfig = config
	result := make(chan error, 1)
	go func() { result <- runtime.Run(t.Context()) }()
	t.Cleanup(func() { release(0); release(1); _ = runtime.Close(); _ = receiveTestValue(t, result) })
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true, MaxConnsPerHost: 1}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	for range 2 {
		go func() {
			defer func() { clients <- struct{}{} }()
			request, requestErr := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+listener.Addr().String(), nil)
			if requestErr != nil {
				t.Error(requestErr)
				return
			}
			response, requestErr := client.Do(request)
			if requestErr == nil {
				_ = response.Body.Close()
			}
		}()
	}
	first, second := receiveTestValue(t, entered), receiveTestValue(t, entered)
	if first.protocol != 2 || second.protocol != 2 || first.peer != second.peer {
		t.Fatalf("not two streams on one native HTTP/2 connection: %v %v", first, second)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	_ = receiveTestValue(t, finalizing)
	_ = receiveTestValue(t, finalizing)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for index := range 2 {
		if err := runtime.WaitHandlers(ctx); !errors.Is(err, serverhttp.ErrHandlersUnjoined) {
			t.Fatalf("joined with stream %d held: %v", index, err)
		}
		release(index)
		_ = receiveTestValue(t, returned)
	}
	if err := runtime.WaitHandlers(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = receiveTestValue(t, clients)
	_ = receiveTestValue(t, clients)
}

func TestHandlerJoinOwnsOuterFinalizersAndRetainsOwnershipAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		listener := &retiringFailureListener{failure: net.ErrClosed, closing: make(chan struct{}), release: make(chan struct{})}
		close(listener.release)
		entered, release := make(chan struct{}), make(chan struct{})
		var unblock sync.Once
		defer unblock.Do(func() { close(release) })
		runtime, err := serverhttp.New(listener, http.NotFoundHandler(),
			serverhttp.WithHandlerJoin(true),
			serverhttp.WithMiddleware(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					defer func() { close(entered); <-release }()
					next.ServeHTTP(writer, request)
				})
			}))
		if err != nil {
			t.Fatal(err)
		}
		returned := make(chan struct{})
		go func() {
			runtime.HTTPServer().Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
			close(returned)
		}()
		<-entered
		cause := errors.New("waiter left")
		left, leave := context.WithCancelCause(context.Background())
		leave(cause)
		if err := runtime.WaitHandlers(left); !errors.Is(err, serverhttp.ErrHandlersUnjoined) || !errors.Is(err, cause) {
			t.Fatalf("canceled waiter lost incomplete ownership or cause: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		err = runtime.WaitHandlers(ctx)
		if !errors.Is(err, serverhttp.ErrHandlersUnjoined) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("WaitHandlers() = %v; want incomplete ownership and deadline", err)
		}
		late := httptest.NewRecorder()
		runtime.HTTPServer().Handler.ServeHTTP(late, httptest.NewRequest(http.MethodGet, "/", nil))
		if late.Code != http.StatusServiceUnavailable {
			t.Fatalf("late request = %d", late.Code)
		}
		unblock.Do(func() { close(release) })
		<-returned
		if err := runtime.WaitHandlers(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRunRetainsServeFailureWhenCancellationWins(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		failure := errors.New("listener failed")
		listener := &retiringFailureListener{
			failure: failure,
			closing: make(chan struct{}),
			release: make(chan struct{}),
		}
		runtime, err := serverhttp.New(listener, http.NotFoundHandler())
		if err != nil {
			t.Fatal(err)
		}
		shutdownStarted := make(chan struct{})
		runtime.HTTPServer().RegisterOnShutdown(func() { close(shutdownStarted) })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var release sync.Once
		defer release.Do(func() { close(listener.release) })
		result := make(chan error, 1)
		go func() { result <- runtime.Run(ctx) }()
		<-listener.closing
		cancel()
		<-shutdownStarted
		synctest.Wait()
		release.Do(func() { close(listener.release) })
		err = <-result
		var serveError *serverhttp.ServeError
		if !errors.Is(err, failure) || !errors.As(err, &serveError) {
			t.Fatalf("Run() = %v; want original typed serving failure alongside cancellation", err)
		}
	})
}

func TestHandlerJoinRejectsDisabledAndNilWaiters(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		listener := &retiringFailureListener{closing: make(chan struct{}), release: make(chan struct{})}
		close(listener.release)
		runtime, err := serverhttp.New(listener, nil, serverhttp.WithHandlerJoin(enabled))
		if err != nil {
			t.Fatal(err)
		}
		//nolint:staticcheck // The public contract explicitly rejects a nil context.
		//lint:ignore SA1012 The public contract explicitly rejects a nil context.
		if err := runtime.WaitHandlers(nil); !errors.Is(err, serverhttp.ErrInvalidConfig) {
			t.Fatalf("nil waiter = %v", err)
		}
		if err := runtime.CloseAdmission(); err != nil {
			t.Fatal(err)
		}
		err = runtime.WaitHandlers(t.Context())
		if !enabled && !errors.Is(err, serverhttp.ErrInvalidState) {
			t.Fatalf("disabled join = %v", err)
		}
		if enabled && err != nil {
			t.Fatal(err)
		}
		if err := runtime.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestHandlerCanCloseAndAttemptBoundedSelfJoin(t *testing.T) {
	listener := &retiringFailureListener{closing: make(chan struct{}), release: make(chan struct{})}
	close(listener.release)
	var runtime *serverhttp.Server
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := runtime.WaitHandlers(ctx); !errors.Is(err, serverhttp.ErrHandlersUnjoined) {
			t.Errorf("self join falsely completed: %v", err)
		}
	})
	var err error
	runtime, err = serverhttp.New(listener, handler, serverhttp.WithHandlerJoin(true))
	if err != nil {
		t.Fatal(err)
	}
	runtime.HTTPServer().Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if err := runtime.WaitHandlers(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Run(t.Context()); !errors.Is(err, serverhttp.ErrInvalidState) {
		t.Fatalf("Run after Close = %v", err)
	}
}

// net/http defers listener.Close after Accept fails. Holding that close keeps
// Serve's result pending so cancellation deterministically wins Run's select.
type retiringFailureListener struct {
	failure error
	closing chan struct{}
	release chan struct{}
	once    sync.Once
}

func (listener *retiringFailureListener) Accept() (net.Conn, error) {
	return nil, listener.failure
}

func (listener *retiringFailureListener) Close() error {
	listener.once.Do(func() { close(listener.closing) })
	<-listener.release
	return nil
}

func (*retiringFailureListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}
}
