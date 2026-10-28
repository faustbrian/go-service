package serverhttp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

func TestRunRetainsConcurrentServingAndCleanupFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		connection, client := net.Pipe()
		defer func() { _ = client.Close() }()
		serveFailure, closeFailure := errors.New("accept failed"), errors.New("forced close failed")
		listener := &cleanupFailureListener{
			connection: connection, failure: serveFailure,
			failAccept: make(chan struct{}), closing: make(chan struct{}), releaseClose: make(chan struct{}),
		}
		entered, releaseHandler := make(chan struct{}), make(chan struct{})
		var acceptOnce, closeOnce, handlerOnce sync.Once
		release := func() {
			acceptOnce.Do(func() { close(listener.failAccept) })
			closeOnce.Do(func() { close(listener.releaseClose) })
			handlerOnce.Do(func() { close(releaseHandler) })
		}
		defer release()
		server, err := New(listener, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(entered)
			<-releaseHandler
		}), WithShutdownTimeout(time.Second), WithHandlerJoin(true))
		if err != nil {
			t.Fatal(err)
		}
		server.close = func() error {
			return errors.Join(server.httpServer.Close(), closeFailure)
		}
		shutdownStarted := make(chan struct{})
		server.httpServer.RegisterOnShutdown(func() { close(shutdownStarted) })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result, clientResult := make(chan error, 1), make(chan error, 1)
		go func() { result <- server.Run(ctx) }()
		go func() {
			_, writeErr := io.WriteString(client, "GET / HTTP/1.1\r\nHost: test\r\n\r\n")
			if writeErr == nil {
				_, writeErr = io.Copy(io.Discard, client)
			}
			clientResult <- writeErr
		}()
		<-entered
		acceptOnce.Do(func() { close(listener.failAccept) })
		<-listener.closing
		cancel()
		<-shutdownStarted
		synctest.Wait()
		closeOnce.Do(func() { close(listener.releaseClose) })
		err = <-result
		var serving *ServeError
		if !errors.Is(err, serveFailure) || !errors.As(err, &serving) ||
			!errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, closeFailure) {
			t.Fatalf("Run() = %v; want serving failure, shutdown deadline and forced-close failure", err)
		}
		if joinErr := server.WaitHandlers(ctx); !errors.Is(joinErr, ErrHandlersUnjoined) {
			t.Fatalf("held handler ownership lost after cleanup failures: %v", joinErr)
		}
		handlerOnce.Do(func() { close(releaseHandler) })
		if err := server.WaitHandlers(context.Background()); err != nil {
			t.Fatal(err)
		}
		<-clientResult
	})
}

// Holding the failed listener's final Close lets cancellation select first,
// while the admitted native HTTP request exhausts graceful shutdown.
type cleanupFailureListener struct {
	connection   net.Conn
	failure      error
	failAccept   chan struct{}
	closing      chan struct{}
	releaseClose chan struct{}
	once         sync.Once
}

func (listener *cleanupFailureListener) Accept() (net.Conn, error) {
	if listener.connection != nil {
		connection := listener.connection
		listener.connection = nil
		return connection, nil
	}
	<-listener.failAccept
	return nil, listener.failure
}

func (listener *cleanupFailureListener) Close() error {
	listener.once.Do(func() { close(listener.closing) })
	<-listener.releaseClose
	return nil
}

func (*cleanupFailureListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestRunAggregatesForcedCloseFailure(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	server, err := New(
		listener,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(requestEntered)
			<-releaseRequest
		}),
		WithShutdownTimeout(time.Nanosecond),
		WithMaxConnections(1),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	closeFailure := errors.New("forced close failed")
	server.close = func() error {
		_ = server.httpServer.Close()

		return closeFailure
	}

	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- server.Run(ctx) }()
	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if requestErr == nil {
			requestErr = response.Body.Close()
		}
		requestResult <- requestErr
	}()
	receiveTestValue(t, requestEntered)
	cancel()
	err = receiveTestValue(t, runResult)
	if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, closeFailure) {
		t.Fatalf("Run() error = %v, want deadline and close failure", err)
	}
	close(releaseRequest)
	if err := receiveTestValue(t, requestResult); err == nil {
		t.Fatal("request error = nil, want forced connection close")
	}
}
