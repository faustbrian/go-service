package referencehttp_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	referencehttp "github.com/faustbrian/go-service/integration/reference-http"
)

// HTTP-CLEAN-1 and HTTP-CLEAN-3: deliberately lose a dial to an idle
// connection, then observe both owned peers close without stopping the server.
func TestReferenceClientOwnedTransportClosesUnusedConnections(t *testing.T) {
	t.Parallel()
	type peerState struct {
		address string
		state   http.ConnState
	}
	states := make(chan peerState, 64)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.Method == http.MethodPost && (r.Header.Get("Signature") == "" || r.Header.Get("Content-Digest") == "") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	server.Config.ConnState = func(c net.Conn, state http.ConnState) {
		states <- peerState{address: c.RemoteAddr().String(), state: state}
	}
	server.Start()
	t.Cleanup(server.Close)
	seen := make(map[string]map[http.ConnState]bool)
	awaitState := func(address string, expected http.ConnState, mustRemainUnused bool) {
		t.Helper()
		deadline := time.NewTimer(5 * time.Second)
		defer deadline.Stop()
		for {
			if mustRemainUnused && seen[address][http.StateActive] {
				t.Fatal("the surplus connection unexpectedly carried a request")
			}
			if seen[address][expected] {
				return
			}
			select {
			case event := <-states:
				if seen[event.address] == nil {
					seen[event.address] = make(map[http.ConnState]bool)
				}
				seen[event.address][event.state] = true
				if event.address != address {
					continue
				}
				if mustRemainUnused && event.state == http.StateActive {
					t.Fatal("the surplus connection unexpectedly carried a request")
				}
				if event.state == expected {
					return
				}
			case <-deadline.C:
				t.Fatalf("peer %s did not reach %s before server shutdown", address, expected)
			}
		}
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	dialed := make(chan net.Conn, 2)
	releaseDial := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseDial) }) }
	t.Cleanup(release)
	var dialCount atomic.Int64
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, address)
		if err != nil {
			return nil, err
		}
		dialed <- conn
		if dialCount.Add(1) == 2 {
			select {
			case <-releaseDial:
			case <-ctx.Done():
				_ = conn.Close()
				return nil, ctx.Err()
			}
		}
		return conn, nil
	}
	management := listen(t)
	t.Cleanup(func() { _ = management.Close() })
	reference, err := referencehttp.New(referencehttp.Config{
		ServiceName: "reference-http", Version: "1.0.0", Environment: "test",
		BearerToken: referenceBearer, PrincipalID: "reference-client", TenantID: referenceTenant,
		BusinessListener: server.Listener, ManagementListener: management,
		TrustTenant: func(*http.Request) bool { return true }, Readiness: func(context.Context) error { return nil },
		ClientTransport: transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reference.Telemetry().Shutdown(context.Background()) })
	request := func() *http.Request {
		r := echoRequest(t, server.Listener, referenceBearer, referenceTenant, "owned")
		if err := reference.PrepareRequest(r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	finish := func(response *http.Response) {
		t.Helper()
		defer func() {
			if err := response.Body.Close(); err != nil {
				t.Error(err)
			}
		}()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("signed request status = %d", response.StatusCode)
		}
		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			t.Fatal(err)
		}
	}
	first, err := reference.Client().Do(request())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Body.Close() })
	var used net.Conn
	select {
	case used = <-dialed:
	case <-time.After(5 * time.Second):
		t.Fatal("signed client did not use its configured transport")
	}
	awaitState(used.LocalAddr().String(), http.StateNew, false)
	secondRequest := request()
	second := make(chan error, 1)
	go func() {
		response, err := reference.Client().Do(secondRequest)
		if err != nil {
			second <- err
			return
		}
		if response.StatusCode != http.StatusOK {
			err = fmt.Errorf("signed request status = %d", response.StatusCode)
		}
		_, readErr := io.Copy(io.Discard, response.Body)
		second <- errors.Join(err, readErr, response.Body.Close())
	}()
	var unused net.Conn
	select {
	case unused = <-dialed:
	case <-time.After(5 * time.Second):
		t.Fatal("configured transport did not start the surplus dial")
	}
	awaitState(unused.LocalAddr().String(), http.StateNew, true)
	finish(first)
	var secondErr error
	select {
	case secondErr = <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("second request did not receive the returned idle connection")
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	independent, err := net.DialTimeout("tcp", server.Listener.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = independent.Close() })
	release()
	transport.CloseIdleConnections()
	awaitState(unused.LocalAddr().String(), http.StateClosed, true)
	awaitState(used.LocalAddr().String(), http.StateClosed, false)
	transport.CloseIdleConnections()

	// The already-open nonowned socket must remain usable after cleanup.
	_ = independent.SetDeadline(time.Now().Add(5 * time.Second))
	probe, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := probe.Write(independent); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(independent), probe)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	finish(response)
}
