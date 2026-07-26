package serverhttp_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

const networkTestDeadline = 5 * time.Second

func TestReadHeaderTimeoutClosesSlowHeaders(t *testing.T) {
	t.Parallel()

	listener, cancel, runResult := startHTTPRuntime(
		t,
		http.NotFoundHandler(),
		serverhttp.WithReadTimeout(0),
		serverhttp.WithReadHeaderTimeout(50*time.Millisecond),
		serverhttp.WithWriteTimeout(0),
	)
	connection := dialHTTPRuntime(t, listener)
	defer func() { _ = connection.Close() }()
	if _, err := io.WriteString(
		connection,
		"GET / HTTP/1.1\r\nHost: example\r\nX-Slow:",
	); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	assertPeerCloses(t, connection)
	stopHTTPRuntime(t, cancel, runResult)
}

func TestReadTimeoutBoundsSlowRequestBody(t *testing.T) {
	t.Parallel()

	readResult := make(chan error, 1)
	listener, cancel, runResult := startHTTPRuntime(
		t,
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_, err := io.ReadAll(request.Body)
			readResult <- err
			http.Error(writer, "request timeout", http.StatusRequestTimeout)
		}),
		serverhttp.WithReadTimeout(50*time.Millisecond),
		serverhttp.WithReadHeaderTimeout(time.Second),
		serverhttp.WithWriteTimeout(0),
	)
	connection := dialHTTPRuntime(t, listener)
	defer func() { _ = connection.Close() }()
	if _, err := io.WriteString(
		connection,
		"POST / HTTP/1.1\r\nHost: example\r\nContent-Length: 1\r\n\r\n",
	); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := <-readResult; err == nil {
		t.Fatal("request body read unexpectedly succeeded")
	}
	stopHTTPRuntime(t, cancel, runResult)
}

func TestWriteTimeoutBoundsUnreadResponse(t *testing.T) {
	t.Parallel()

	writeResult := make(chan error, 1)
	listener, cancel, runResult := startHTTPRuntime(
		t,
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			chunk := make([]byte, 1<<20)
			controller := http.NewResponseController(writer)
			for {
				if _, err := writer.Write(chunk); err != nil {
					writeResult <- err

					return
				}
				if err := controller.Flush(); err != nil {
					writeResult <- err

					return
				}
			}
		}),
		serverhttp.WithReadTimeout(0),
		serverhttp.WithReadHeaderTimeout(time.Second),
		serverhttp.WithWriteTimeout(50*time.Millisecond),
	)
	connection := dialHTTPRuntime(t, listener)
	defer func() { _ = connection.Close() }()
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\nHost: example\r\n\r\n"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	select {
	case err := <-writeResult:
		if err == nil {
			t.Fatal("response write error = nil")
		}
	case <-time.After(networkTestDeadline):
		t.Fatal("response write was not bounded")
	}
	stopHTTPRuntime(t, cancel, runResult)
}

func TestIdleTimeoutClosesKeepAliveConnection(t *testing.T) {
	t.Parallel()

	listener, cancel, runResult := startHTTPRuntime(
		t,
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, "ok")
		}),
		serverhttp.WithReadTimeout(0),
		serverhttp.WithReadHeaderTimeout(time.Second),
		serverhttp.WithWriteTimeout(0),
		serverhttp.WithIdleTimeout(50*time.Millisecond),
	)
	connection := dialHTTPRuntime(t, listener)
	defer func() { _ = connection.Close() }()
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\nHost: example\r\n\r\n"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatalf("response body read error = %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("response body close error = %v", err)
	}
	assertPeerCloses(t, connection)
	stopHTTPRuntime(t, cancel, runResult)
}

func TestMaxHeaderBytesRejectsHostileHeader(t *testing.T) {
	t.Parallel()

	handlerCalled := make(chan struct{}, 1)
	listener, cancel, runResult := startHTTPRuntime(
		t,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			handlerCalled <- struct{}{}
		}),
		serverhttp.WithMaxHeaderBytes(64),
	)
	connection := dialHTTPRuntime(t, listener)
	defer func() { _ = connection.Close() }()
	request := "GET / HTTP/1.1\r\nHost: example\r\nX-Large: " +
		strings.Repeat("a", 8<<10) + "\r\n\r\n"
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusRequestHeaderFieldsTooLarge)
	}
	select {
	case <-handlerCalled:
		t.Fatal("handler received an oversized header")
	default:
	}
	stopHTTPRuntime(t, cancel, runResult)
}

func TestClientDisconnectCancelsRequest(t *testing.T) {
	t.Parallel()

	handlerEntered := make(chan struct{})
	handlerCanceled := make(chan error, 1)
	listener, cancel, runResult := startHTTPRuntime(
		t,
		http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			close(handlerEntered)
			<-request.Context().Done()
			handlerCanceled <- context.Cause(request.Context())
		}),
	)
	connection := dialHTTPRuntime(t, listener)
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\nHost: example\r\n\r\n"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	<-handlerEntered
	if err := connection.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if cause := <-handlerCanceled; !errors.Is(cause, context.Canceled) {
		t.Fatalf("request cause = %v, want context.Canceled", cause)
	}
	stopHTTPRuntime(t, cancel, runResult)
}

func TestRecoveryPreservesHijacking(t *testing.T) {
	t.Parallel()

	hijackResult := make(chan error, 1)
	listener, cancel, runResult := startHTTPRuntime(
		t,
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			connection, buffered, err := http.NewResponseController(writer).Hijack()
			if err != nil {
				hijackResult <- err

				return
			}
			defer func() { _ = connection.Close() }()
			_, err = buffered.WriteString(
				"HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok",
			)
			if err == nil {
				err = buffered.Flush()
			}
			hijackResult <- err
		}),
	)
	connection := dialHTTPRuntime(t, listener)
	defer func() { _ = connection.Close() }()
	if _, err := io.WriteString(connection, "GET / HTTP/1.1\r\nHost: example\r\n\r\n"); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatalf("ReadResponse() error = %v", err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("response body read error = %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("response body close error = %v", err)
	}
	if string(body) != "ok" {
		t.Fatalf("response body = %q, want ok", body)
	}
	if err := <-hijackResult; err != nil {
		t.Fatalf("Hijack() path error = %v", err)
	}
	stopHTTPRuntime(t, cancel, runResult)
}

func TestRunSupportsStandardLibraryUnencryptedHTTP2(t *testing.T) {
	t.Parallel()

	protocol := make(chan string, 1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	runtime, err := serverhttp.New(listener, http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		protocol <- request.Proto
		_, _ = io.WriteString(writer, "ok")
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	serverProtocols := new(http.Protocols)
	serverProtocols.SetHTTP1(true)
	serverProtocols.SetUnencryptedHTTP2(true)
	runtime.HTTPServer().Protocols = serverProtocols
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(ctx) }()

	clientProtocols := new(http.Protocols)
	clientProtocols.SetUnencryptedHTTP2(true)
	transport := &http.Transport{Protocols: clientProtocols}
	client := &http.Client{Transport: transport, Timeout: networkTestDeadline}
	response, err := client.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("response body close error = %v", err)
	}
	if got := <-protocol; got != "HTTP/2.0" {
		t.Fatalf("request protocol = %q, want HTTP/2.0", got)
	}
	transport.CloseIdleConnections()
	stopHTTPRuntime(t, cancel, runResult)
}

func startHTTPRuntime(
	t *testing.T,
	handler http.Handler,
	options ...serverhttp.Option,
) (net.Listener, context.CancelFunc, <-chan error) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	options = append(options, serverhttp.WithShutdownTimeout(time.Second))
	runtime, err := serverhttp.New(listener, handler, options...)
	if err != nil {
		_ = listener.Close()
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(ctx) }()

	return listener, cancel, runResult
}

func dialHTTPRuntime(t *testing.T, listener net.Listener) net.Conn {
	t.Helper()

	connection, err := net.DialTimeout("tcp", listener.Addr().String(), networkTestDeadline)
	if err != nil {
		t.Fatalf("Dial() error = %v", err)
	}
	if err := connection.SetDeadline(time.Now().Add(networkTestDeadline)); err != nil {
		_ = connection.Close()
		t.Fatalf("SetDeadline() error = %v", err)
	}

	return connection
}

func assertPeerCloses(t *testing.T, connection net.Conn) {
	t.Helper()

	buffer := make([]byte, 1024)
	for {
		_, err := connection.Read(buffer)
		if err == nil {
			continue
		}
		var networkError net.Error
		if errors.As(err, &networkError) && networkError.Timeout() {
			t.Fatalf("peer did not close before the test deadline: %v", err)
		}

		return
	}
}

func stopHTTPRuntime(t *testing.T, cancel context.CancelFunc, runResult <-chan error) {
	t.Helper()

	cancel()
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(networkTestDeadline):
		t.Fatal("Run() did not stop")
	}
}
