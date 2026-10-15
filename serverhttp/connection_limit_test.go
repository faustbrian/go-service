package serverhttp_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/go-service/v2/serverhttp"
)

type acceptJournal struct {
	net.Listener
	accepted chan net.Conn
}

type temporaryAcceptError struct{}

func (temporaryAcceptError) Error() string   { return "temporary accept failure" }
func (temporaryAcceptError) Timeout() bool   { return false }
func (temporaryAcceptError) Temporary() bool { return true }

type transientAcceptListener struct {
	net.Listener
	first bool
}

type closedWithErrorConn struct{ net.Conn }

func (connection closedWithErrorConn) Close() error {
	_ = connection.Conn.Close()
	return errors.New("connection close reported failure")
}

type closeErrorConnListener struct{ net.Listener }

func (listener closeErrorConnListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return closedWithErrorConn{connection}, nil
}

func TestConnectionLimitReleasesTerminalCloseError(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := serverhttp.New(closeErrorConnListener{listener}, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(204) }), serverhttp.WithMaxConnections(1))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := receiveTestValue(t, done); err != nil {
			t.Error(err)
		}
	})
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	for range 2 {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatal("closed connection retained admission", err)
		}
		_ = response.Body.Close()
		if response.StatusCode != 204 {
			t.Fatal(response.StatusCode)
		}
	}
}

func (listener *transientAcceptListener) Accept() (net.Conn, error) {
	if !listener.first {
		listener.first = true
		return nil, temporaryAcceptError{}
	}
	return listener.Listener.Accept()
}

func TestConnectionLimitReleasesFailedAcceptAndRetainsObserver(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := serverhttp.New(&transientAcceptListener{Listener: listener}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), serverhttp.WithMaxConnections(1))
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{}, 1)
	runtime.HTTPServer().ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			closed <- struct{}{}
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := receiveTestValue(t, done); err != nil {
			t.Error(err)
		}
	})
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	receiveTestValue(t, closed)
}

func TestConnectionLimitSaturatedConcurrentClose(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	journal := &acceptJournal{Listener: listener, accepted: make(chan net.Conn, 2)}
	runtime, err := serverhttp.New(journal, nil, serverhttp.WithMaxConnections(1))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	first, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	receiveTestValue(t, journal.accepted)
	pending, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pending.Close() }()
	var closers sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		closers.Go(func() { results <- runtime.Close() })
	}
	closers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Error(err)
		}
	}
	if err := receiveTestValue(t, done); err != nil {
		t.Error(err)
	}
	if err := first.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := first.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("owned idle socket not closed: %v", err)
	}
}

func TestConnectionLimitInvalidConstructionKeepsCallerListener(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	if _, err := serverhttp.New(listener, nil, serverhttp.WithMaxConnections(-1)); !errors.Is(err, serverhttp.ErrInvalidConfig) {
		t.Fatalf("invalid bound not rejected: %v", err)
	}
	// Successfully construct and serve through the same listener after rejection.
	runtime, err := serverhttp.New(listener, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), serverhttp.WithMaxConnections(0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := receiveTestValue(t, done); err != nil {
			t.Error(err)
		}
	})
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}, Timeout: time.Second}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
}

func TestConnectionLimitTransfersHijackedOwnership(t *testing.T) {
	transferred := make(chan net.Conn, 1)
	listener, cancel, done := startHTTPRuntime(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/hijack" {
			connection, _, err := http.NewResponseController(writer).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			transferred <- connection
			return
		}
		writer.WriteHeader(204)
	}), serverhttp.WithMaxConnections(1))
	clientConn := dialHTTPRuntime(t, listener)
	defer func() { _ = clientConn.Close() }()
	if _, err := io.WriteString(clientConn, "GET /hijack HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	callerOwned := receiveTestValue(t, transferred)
	defer func() { _ = callerOwned.Close() }()
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+listener.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal("hijack retained server admission")
	}
	stopHTTPRuntime(t, cancel, done)
	if err := callerOwned.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := clientConn.Write([]byte("x")); err != nil {
		t.Fatal("runtime closed caller-owned hijacked connection")
	}
	buffer := make([]byte, 1)
	if _, err := io.ReadFull(callerOwned, buffer); err != nil || buffer[0] != 'x' {
		t.Fatalf("hijacked owner lost its socket: %v", err)
	}
}

func (listener *acceptJournal) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err == nil {
		listener.accepted <- connection
	}
	return connection, err
}

// TCP Dial may complete into the OS backlog. The journal observes actual
// accepted sockets before net/http creates per-connection work.
func TestConnectionLimitBoundsIdleAdmissionAndReleases(t *testing.T) {
	for _, maximum := range []int{1, 2} {
		for _, encrypted := range []bool{false, true} {
			t.Run(fmt.Sprintf("maximum=%d/tls=%t", maximum, encrypted), func(t *testing.T) {
				base, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				listener := base
				var clientTLS *tls.Config
				if encrypted {
					certificateServer := httptest.NewTLSServer(http.NotFoundHandler())
					roots := x509.NewCertPool()
					roots.AddCert(certificateServer.Certificate())
					clientTLS = &tls.Config{RootCAs: roots, ServerName: "example.com", MinVersion: tls.VersionTLS12}
					config := certificateServer.TLS.Clone()
					certificateServer.Close()
					listener = tls.NewListener(listener, config)
				}
				journal := &acceptJournal{Listener: listener, accepted: make(chan net.Conn, maximum+2)}
				runtime, err := serverhttp.New(journal, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
					if (request.TLS != nil) != encrypted {
						t.Error("native TLS metadata lost")
					}
					_, _ = io.WriteString(writer, "bounded")
				}), serverhttp.WithMaxConnections(maximum), serverhttp.WithShutdownTimeout(time.Second))
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
						t.Error("saturated server failed to stop")
					}
				})
				dial := func() net.Conn {
					t.Helper()
					client, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", base.Addr().String())
					if err != nil {
						t.Fatal(err)
					}
					clients = append(clients, client)
					return client
				}
				awaitAccept := func() {
					t.Helper()
					select {
					case <-journal.accepted:
					case <-time.After(time.Second):
						t.Fatal("admission did not progress")
					}
				}
				for range maximum {
					dial()
					awaitAccept()
				}
				pending := dial()
				select {
				case <-journal.accepted:
					t.Fatal("idle sockets exceeded finite connection capacity")
				case <-time.After(50 * time.Millisecond):
				}
				if err := clients[0].Close(); err != nil {
					t.Fatal(err)
				}
				_ = clients[0].Close()
				awaitAccept()
				if encrypted {
					secured := tls.Client(pending, clientTLS)
					handshake, cancelHandshake := context.WithTimeout(t.Context(), time.Second)
					err := secured.HandshakeContext(handshake)
					cancelHandshake()
					if err != nil {
						t.Fatal(err)
					}
					pending = secured
				}
				if err := pending.SetDeadline(time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.com/", nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Close = true
				if err := request.Write(pending); err != nil {
					t.Fatal(err)
				}
				response, err := http.ReadResponse(bufio.NewReader(pending), request)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if err != nil || response.StatusCode != 200 || string(body) != "bounded" {
					t.Fatalf("admitted response failed: status=%d error=%v", response.StatusCode, err)
				}
			})
		}
	}
}
