package serverhttp_test

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/faustbrian/go-service/v3/serverhttp"
)

func TestPanicRecoveryPolicyControlsContainmentOnly(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		options []serverhttp.Option
		recover bool
	}{
		{"default", nil, true},
		{"native", []serverhttp.Option{serverhttp.WithPanicRecovery(false)}, false},
		{"reenabled", []serverhttp.Option{serverhttp.WithPanicRecovery(false), serverhttp.WithPanicRecovery(true)}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			var order []string
			middleware := func(name string) serverhttp.Middleware {
				return func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						order = append(order, name)
						next.ServeHTTP(w, r)
					})
				}
			}
			panicValue := &struct{ message string }{message: "private panic sentinel"}
			runtime, err := serverhttp.New(listener, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				panic(panicValue)
			}), append(test.options,
				serverhttp.WithIngressMiddleware(middleware("ingress")),
				serverhttp.WithBodyLimit(1),
				serverhttp.WithMiddleware(middleware("handler")),
			)...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close() })
			response := httptest.NewRecorder()
			var got any
			func() {
				defer func() { got = recover() }()
				runtime.HTTPServer().Handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			}()
			if test.recover {
				if got != nil || response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), panicValue.message) {
					t.Fatal("enabled recovery did not contain and redact panic")
				}
			} else if got != panicValue {
				t.Fatal("disabled recovery changed the caller's panic")
			}
			if !reflect.DeepEqual(order, []string{"ingress", "handler"}) {
				t.Fatalf("middleware order = %v", order)
			}
			order = nil
			response = httptest.NewRecorder()
			runtime.HTTPServer().Handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("xx")))
			if response.Code != http.StatusRequestEntityTooLarge || !reflect.DeepEqual(order, []string{"ingress"}) {
				t.Fatal("recovery selection bypassed body admission or changed ingress order")
			}
		})
	}
}

func TestDisabledPanicRecoveryMatchesNativeHTTPFailure(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		committed bool
		http2     bool
	}{
		{"HTTP1 before commit", false, false},
		{"HTTP1 after flush", true, false},
		{"HTTP2 before commit", false, true},
		{"HTTP2 after flush", true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if (r.ProtoMajor == 2) != test.http2 {
					t.Error("request did not use the selected protocol")
				}
				if r.URL.Path == "/alive" {
					_, _ = io.WriteString(w, "alive")
					return
				}
				if test.committed {
					if _, err := io.WriteString(w, "prefix"); err != nil {
						t.Error(err)
						return
					}
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Error(err)
						return
					}
				}
				panic("private panic sentinel")
			})
			native := httptest.NewUnstartedServer(handler)
			native.Config.ErrorLog = log.New(io.Discard, "", 0)
			protocols := new(http.Protocols)
			protocols.SetHTTP1(!test.http2)
			protocols.SetUnencryptedHTTP2(test.http2)
			native.Config.Protocols = protocols
			native.Start()
			t.Cleanup(native.Close)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			runtime, err := serverhttp.New(listener, handler, serverhttp.WithPanicRecovery(false), serverhttp.WithShutdownTimeout(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			runtime.HTTPServer().ErrorLog = log.New(io.Discard, "", 0)
			runtime.HTTPServer().Protocols = protocols
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- runtime.Run(ctx) }()
			t.Cleanup(func() { stopHTTPRuntime(t, cancel, done) })
			transport := &http.Transport{DisableKeepAlives: true, Protocols: protocols}
			t.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: networkTestDeadline}
			for _, address := range []string{native.URL, "http://" + listener.Addr().String()} {
				response, err := client.Get(address + "/panic")
				if !test.committed {
					if response != nil {
						_ = response.Body.Close()
					}
					if response != nil || err == nil || (!test.http2 && !errors.Is(err, io.EOF)) {
						t.Fatal("uncommitted panic became an HTTP response instead of a connection abort")
					}
					var timeout net.Error
					if errors.As(err, &timeout) && timeout.Timeout() {
						t.Fatal("request timed out instead of aborting")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					body, readErr := io.ReadAll(response.Body)
					_ = response.Body.Close()
					if response.StatusCode != http.StatusOK || string(body) != "prefix" || readErr == nil || (!test.http2 && !errors.Is(readErr, io.ErrUnexpectedEOF)) {
						t.Fatal("committed panic became a successful complete response")
					}
					var timeout net.Error
					if errors.As(readErr, &timeout) && timeout.Timeout() {
						t.Fatal("partial response timed out instead of aborting")
					}
				}
				response, err = client.Get(address + "/alive")
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if readErr != nil || string(body) != "alive" {
					t.Fatal("request panic stopped the server")
				}
			}
		})
	}
}
