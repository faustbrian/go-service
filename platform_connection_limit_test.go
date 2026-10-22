package service_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/faustbrian/go-service/v3"
	"github.com/faustbrian/go-service/v3/serverhttp"
)

func TestCanonicalBusinessConnectionLimit(t *testing.T) {
	for _, provided := range []bool{false, true} {
		t.Run(fmt.Sprintf("provided-listener=%t", provided), func(t *testing.T) {
			business, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			address := business.Addr().String()
			management, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			accepted := make(chan struct{}, 3)
			httpPlan := &service.HTTP{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				assertCorrelationContext(t, request.Context())
				_, _ = io.WriteString(writer, "canonical")
			}), Options: []serverhttp.Option{serverhttp.WithMaxConnections(1), serverhttp.WithShutdownTimeout(time.Second), serverhttp.WithConnContext(func(ctx context.Context, _ net.Conn) context.Context { accepted <- struct{}{}; return ctx })}}
			if provided {
				httpPlan.Listener = business
			} else {
				httpPlan.Address = address
				if err := business.Close(); err != nil {
					t.Fatal(err)
				}
			}
			role := service.CommandFor(service.CommandSpec[struct{}]{Name: "serve", Kind: service.CommandKindLongRunning, Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil }, Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
				return service.Plan{HTTP: httpPlan}, nil
			}})
			ready := make(chan struct{})
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan int, 1)
			go func() {
				done <- service.Execute(ctx, service.Definition{Identity: service.Identity{Name: "bounded"}, Observer: service.RuntimeObserverFunc(func(_ context.Context, event service.RuntimeEvent) {
					if event.Kind == service.RuntimeEventReadiness && event.Result == service.RuntimeResultAvailable {
						close(ready)
					}
				}), Commands: service.Commands{Serve: role}, Management: service.Management{Listener: management}}, service.Invocation{Args: []string{"serve"}, Stdout: io.Discard, Stderr: io.Discard})
			}()
			var clients []net.Conn
			t.Cleanup(func() {
				for _, client := range clients {
					_ = client.Close()
				}
				cancel()
				if exit := receiveTestValue(t, done); exit != 0 {
					t.Errorf("canonical shutdown exit=%d", exit)
				}
			})
			transport := &http.Transport{DisableKeepAlives: true}
			defer transport.CloseIdleConnections()
			probeClient := &http.Client{Transport: transport, Timeout: time.Second}
			probe := func() {
				t.Helper()
				request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+management.Addr().String()+"/readyz", nil)
				response, err := probeClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				_, err = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if err != nil || response.StatusCode != 200 {
					t.Fatal("independent management readiness unavailable")
				}
			}
			receiveTestValue(t, ready)
			probe()
			dial := func() net.Conn {
				t.Helper()
				client, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", address)
				if err != nil {
					t.Fatal(err)
				}
				clients = append(clients, client)
				return client
			}
			first := dial()
			receiveTestValue(t, accepted)
			next := dial()
			select {
			case <-accepted:
				t.Fatal("canonical HTTP dropped connection admission")
			case <-time.After(50 * time.Millisecond):
			}
			probe()
			_ = first.Close()
			receiveTestValue(t, accepted)
			if err := next.SetDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			request, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+address, nil)
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
			if err != nil || string(body) != "canonical" {
				t.Fatal("canonical admitted request failed")
			}
		})
	}
}
