package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/go-service/v3"
	"github.com/faustbrian/go-service/v3/serverhttp"
)

func TestExecuteRetainsDependenciesUntilEveryHTTPHandlerReturns(t *testing.T) {
	for _, mode := range []string{"graceful", "forced-close", "serving-failure", "overall-timeout", "client-before-term", "client-after-term"} {
		t.Run(mode, func(t *testing.T) { testHTTPFinalizationOwnership(t, mode) })
	}
}

func TestHTTPHandlerJoinPreservesNeverRunOwnership(t *testing.T) {
	for _, join := range []bool{false, true} {
		for _, rollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("join=%t/rollback=%t", join, rollback), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				listen := func() *closeObservedListener {
					t.Helper()
					listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
					if err != nil {
						t.Fatal(err)
					}
					observed := &closeObservedListener{Listener: listener, closed: make(chan struct{})}
					t.Cleanup(func() { _ = observed.Close() })
					return observed
				}
				business, management := listen(), listen()
				var stops atomic.Int32
				command := service.CommandFor(service.CommandSpec[struct{}]{
					Name: "serve", Kind: service.CommandKindLongRunning,
					Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil },
					Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
						components := []service.Component{{Name: "database", Stop: func(context.Context) error {
							stops.Add(1)
							return nil
						}}}
						if rollback {
							components = append(components, service.Component{Name: "later", Start: func(context.Context) error {
								return errors.New("startup fixture failure")
							}})
						}
						return service.Plan{Components: components, ShutdownTimeout: time.Second, HTTP: &service.HTTP{
							Listener: business, JoinHandlers: join, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
								t.Error("never-run handler was admitted")
							}),
						}}, nil
					},
				})
				exit := service.Execute(ctx, service.Definition{
					Identity: service.Identity{Name: "never-run"}, Commands: service.Commands{Serve: command},
					Management: service.Management{Listener: management},
					Observer: service.RuntimeObserverFunc(func(_ context.Context, event service.RuntimeEvent) {
						if !rollback && event.Kind == service.RuntimeEventStartup && event.Result == service.RuntimeResultSucceeded {
							// Startup has acquired HTTP, but no task has been registered.
							cancel()
						}
					}),
				}, service.Invocation{Args: []string{"serve"}, Stdout: io.Discard, Stderr: io.Discard})
				if exit == 0 || stops.Load() != 1 {
					t.Fatalf("never-run cleanup exit=%d stops=%d", exit, stops.Load())
				}
				select {
				case <-management.closed:
				default:
					t.Error("owned management listener leaked")
				}
				select {
				case <-business.closed:
					if rollback {
						t.Error("rollback closed an unacquired caller-owned listener")
					}
				default:
					if !rollback {
						t.Error("task-registration cancellation leaked acquired HTTP listener")
					}
				}
			})
		}
	}
}

func testHTTPFinalizationOwnership(t *testing.T, mode string) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	listen := func() net.Listener {
		t.Helper()
		listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		return listener
	}
	business := &closeObservedListener{Listener: listen(), closed: make(chan struct{})}
	management := listen()
	entered, finalizing := make(chan struct{}, 2), make(chan struct{}, 2)
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var releaseOnce [2]sync.Once
	release := func(index int) { releaseOnce[index].Do(func() { close(releases[index]) }) }
	returned := make(chan int, 2)
	stopped := make(chan struct{})
	draining := make(chan struct{})
	allowance := time.Second
	if mode == "overall-timeout" {
		allowance = 150 * time.Millisecond
	}
	httpAllowance := 25 * time.Millisecond
	if mode == "graceful" || strings.HasPrefix(mode, "client-") {
		httpAllowance = time.Second
		allowance = 2 * time.Second
	}
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		index, err := strconv.Atoi(request.URL.Path[1:])
		if err != nil || index < 0 || index >= len(releases) {
			t.Error("invalid fixture request")
			return
		}
		entered <- struct{}{}
		if mode == "graceful" {
			<-releases[index]
			if request.Context().Err() != nil {
				t.Error("retirement canceled an admitted request during HTTP grace")
			}
			_, _ = io.WriteString(writer, "complete-"+strconv.Itoa(index))
			returned <- index
			return
		}
		<-request.Context().Done()
		finalizing <- struct{}{}
		<-releases[index]
		returned <- index
	})
	command := service.CommandFor(service.CommandSpec[struct{}]{
		Name: "serve", Kind: service.CommandKindLongRunning,
		Load: func(context.Context, service.Invocation) (struct{}, error) { return struct{}{}, nil },
		Build: func(context.Context, service.BuildContext, struct{}) (service.Plan, error) {
			return service.Plan{
				ShutdownTimeout: allowance,
				Tasks: []service.Task{{Name: "shutdown-observer", Run: func(ctx context.Context) error {
					<-ctx.Done()
					close(draining)
					return nil
				}}},
				Components: []service.Component{{Name: "database", Stop: func(context.Context) error {
					close(stopped)
					return nil
				}}},
				HTTP: &service.HTTP{Listener: business, Handler: handler, JoinHandlers: true, Options: []serverhttp.Option{
					serverhttp.WithShutdownTimeout(httpAllowance),
					serverhttp.WithConnContext(func(ctx context.Context, _ net.Conn) context.Context {
						return context.WithoutCancel(ctx)
					}),
				}},
			}, nil
		},
	})
	exited := make(chan struct{})
	var exit int
	go func() {
		defer close(exited)
		exit = service.Execute(ctx, service.Definition{
			Identity: service.Identity{Name: "http-finalization"},
			Commands: service.Commands{Serve: command}, Management: service.Management{Listener: management},
		}, service.Invocation{Args: []string{"serve"}, Stdout: io.Discard, Stderr: io.Discard})
	}()
	t.Cleanup(func() {
		cancel()
		release(0)
		release(1)
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			t.Error("HTTP owner failed to finish")
		}
	})
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	requestContext, disconnect := context.WithCancel(t.Context())
	defer disconnect()
	clients := make(chan struct{}, 2)
	for index := range 2 {
		go func() {
			defer func() { clients <- struct{}{} }()
			request, err := http.NewRequestWithContext(requestContext, http.MethodGet, "http://"+business.Addr().String()+"/"+strconv.Itoa(index), nil)
			if err != nil {
				t.Error(err)
				return
			}
			response, err := client.Do(request)
			if err == nil {
				body, readErr := io.ReadAll(response.Body)
				_ = response.Body.Close()
				if mode == "graceful" && (readErr != nil || string(body) != "complete-"+strconv.Itoa(index)) {
					t.Errorf("retiring request response = %q, error=%v", body, readErr)
				}
			} else if mode == "graceful" {
				t.Errorf("retiring request failed: %v", err)
			}
		}()
	}
	await := func(channel <-chan struct{}, description string) {
		t.Helper()
		select {
		case <-channel:
		case <-time.After(time.Second):
			t.Fatalf("did not observe %s", description)
		}
	}
	await(entered, "first admitted handler")
	await(entered, "second admitted handler")
	if mode == "client-before-term" {
		disconnect()
		await(finalizing, "first disconnected finalizer")
		await(finalizing, "second disconnected finalizer")
	}
	if mode == "serving-failure" {
		if err := business.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		cancel()
	}
	if mode == "graceful" || mode == "client-after-term" {
		await(business.closed, "listener withdrawal")
	}
	if mode == "client-after-term" {
		disconnect()
	}
	if mode != "graceful" && mode != "client-before-term" {
		await(finalizing, "first canceled-request finalizer")
		await(finalizing, "second canceled-request finalizer")
	}
	await(draining, "shutdown initiation before finalization completes")
	if mode == "overall-timeout" {
		await(exited, "bounded overall timeout")
		if exit != 124 {
			t.Errorf("held finalization exit = %d, want timeout 124", exit)
		}
	}
	assertOwned := func() {
		t.Helper()
		select {
		case <-stopped:
			t.Error("dependencies closed while HTTP finalization is held")
		case <-time.After(50 * time.Millisecond):
		}
		if mode != "overall-timeout" {
			select {
			case <-exited:
				t.Error("command returned while HTTP finalization is held")
			default:
			}
		}
	}
	assertOwned()
	release(0)
	select {
	case index := <-returned:
		if index != 0 {
			t.Fatal("wrong handler completed first")
		}
	case <-time.After(time.Second):
		t.Fatal("first handler did not return")
	}
	assertOwned()
	release(1)
	await(exited, "HTTP owner completion")
	await(stopped, "dependency cleanup")
	await(clients, "first client completion")
	await(clients, "second client completion")
	if (mode == "forced-close" || mode == "serving-failure") && exit == 0 {
		t.Error("forced HTTP drain failure was hidden")
	}
	if (mode == "graceful" || strings.HasPrefix(mode, "client-")) && exit != 0 {
		t.Errorf("successful HTTP retirement exit = %d", exit)
	}
}
