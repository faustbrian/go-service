package service_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/faustbrian/go-service/v3"
)

type ownedResource struct {
	id     int
	closes atomic.Int32
	close  func(context.Context) error
}

func (resource *ownedResource) Close(ctx context.Context) error {
	resource.closes.Add(1)
	if resource.close != nil {
		return resource.close(ctx)
	}
	return nil
}

type closingInterface interface {
	Close(context.Context) error
}

func TestOwnedRejectsInvalidConfig(t *testing.T) {
	open := func(context.Context) (*ownedResource, error) { return &ownedResource{}, nil }
	cases := map[string]error{}
	_, cases["blank name"] = service.NewOwned(service.OwnedConfig[*ownedResource]{Name: " ", Open: open})
	_, cases["nil open"] = service.NewOwned(service.OwnedConfig[*ownedResource]{Name: "resource"})
	_, cases["negative timeout"] = service.NewOwned(service.OwnedConfig[*ownedResource]{Name: "resource", Open: open, CloseTimeout: -1})
	_, cases["no closer"] = service.NewOwned(service.OwnedConfig[int]{
		Name: "resource", Open: func(context.Context) (int, error) { return 1, nil },
	})
	for name, err := range cases {
		var configError *service.ConfigError
		if !errors.Is(err, service.ErrInvalidConfig) || !errors.As(err, &configError) {
			t.Errorf("%s: error = %v, want ConfigError", name, err)
		}
	}
	if _, err := service.NewOwned(service.OwnedConfig[closingInterface]{
		Name: "resource", Open: func(context.Context) (closingInterface, error) { return &ownedResource{}, nil },
	}); err != nil {
		t.Errorf("interface resource with Close method rejected: %v", err)
	}
	if _, err := service.NewOwned(service.OwnedConfig[int]{
		Name: "resource", Open: func(context.Context) (int, error) { return 1, nil },
		Close: func(context.Context, int) error { return nil },
	}); err != nil {
		t.Errorf("value resource with explicit Close rejected: %v", err)
	}
}

func TestOwnedServiceLifecycle(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		resource := &ownedResource{id: 7}
		var closeDeadline time.Time
		resource.close = func(ctx context.Context) error {
			closeDeadline, _ = ctx.Deadline()
			return nil
		}
		var opens atomic.Int32
		owned, err := service.NewOwned(service.OwnedConfig[*ownedResource]{
			Name: "resource",
			Open: func(context.Context) (*ownedResource, error) {
				opens.Add(1)
				return resource, nil
			},
			CloseTimeout: 3 * time.Second,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := owned.Current(); !errors.Is(err, service.ErrResourceNotStarted) {
			t.Fatalf("current before start = %v", err)
		}
		runtime, err := service.New(service.Config{Components: []service.Component{owned.Component()}})
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if current, err := owned.Current(); err != nil || current != resource {
			t.Fatalf("current after start = %v, %v", current, err)
		}
		if err := owned.Component().Start(context.Background()); !errors.Is(err, service.ErrResourceAlreadyStarted) {
			t.Fatalf("second start = %v", err)
		}
		stopStarted := time.Now()
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !closeDeadline.Equal(stopStarted.Add(3 * time.Second)) {
			t.Errorf("close deadline = %v, want stop plus CloseTimeout %v", closeDeadline, stopStarted.Add(3*time.Second))
		}
		if _, err := owned.Current(); !errors.Is(err, service.ErrResourceClosed) {
			t.Fatalf("current after stop = %v", err)
		}
		if err := owned.Component().Stop(context.Background()); err != nil {
			t.Fatalf("repeated stop = %v", err)
		}
		if err := owned.Component().Start(context.Background()); !errors.Is(err, service.ErrResourceClosed) {
			t.Fatalf("start after stop = %v", err)
		}
		if opens.Load() != 1 || resource.closes.Load() != 1 {
			t.Fatalf("opens = %d closes = %d, want 1 each", opens.Load(), resource.closes.Load())
		}
	})
}

func TestOwnedOpenFailureLeavesResourceRetryable(t *testing.T) {
	openErr := errors.New("open failed")
	attempt := 0
	var resources []*ownedResource
	owned, err := service.NewOwned(service.OwnedConfig[*ownedResource]{
		Name: "resource",
		Open: func(context.Context) (*ownedResource, error) {
			attempt++
			switch attempt {
			case 1:
				return nil, openErr
			case 2:
				return nil, nil
			case 3:
				panic("open exploded")
			}
			resource := &ownedResource{id: attempt}
			resources = append(resources, resource)
			return resource, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	component := owned.Component()
	if err := component.Start(context.Background()); !errors.Is(err, openErr) {
		t.Fatalf("failed open = %v", err)
	}
	if err := component.Start(context.Background()); !errors.Is(err, service.ErrResourceNil) {
		t.Fatalf("nil open = %v", err)
	}
	var panicError *service.PanicError
	if err := component.Start(context.Background()); !errors.As(err, &panicError) || panicError.Operation != "open" {
		t.Fatalf("panicking open = %v", err)
	}
	if _, err := owned.Current(); !errors.Is(err, service.ErrResourceNotStarted) {
		t.Fatalf("current after failed opens = %v", err)
	}
	if err := component.Start(context.Background()); err != nil {
		t.Fatalf("retry after failed opens = %v", err)
	}
	if current, err := owned.Current(); err != nil || current.id != 4 {
		t.Fatalf("current after retry = %v, %v", current, err)
	}
	if err := component.Stop(context.Background()); err != nil || resources[0].closes.Load() != 1 {
		t.Fatalf("stop = %v closes = %d", err, resources[0].closes.Load())
	}
}

func TestOwnedRejectsNilContexts(t *testing.T) {
	owned, err := service.NewOwned(service.OwnedConfig[*ownedResource]{
		Name: "resource", Open: func(context.Context) (*ownedResource, error) { return &ownedResource{}, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	component := owned.Component()
	//nolint:staticcheck // The nil context is the rejected input under test.
	if err := component.Start(nil); !errors.Is(err, service.ErrInvalidConfig) {
		t.Errorf("nil start context = %v", err)
	}
	//nolint:staticcheck // The nil context is the rejected input under test.
	if err := component.Stop(nil); !errors.Is(err, service.ErrInvalidConfig) {
		t.Errorf("nil stop context = %v", err)
	}
	if _, err := owned.Current(); !errors.Is(err, service.ErrResourceNotStarted) {
		t.Errorf("nil contexts changed state: %v", err)
	}
}

func TestOwnedStopBeforeStartIsTerminal(t *testing.T) {
	var opens atomic.Int32
	owned, err := service.NewOwned(service.OwnedConfig[*ownedResource]{
		Name: "resource", Open: func(context.Context) (*ownedResource, error) {
			opens.Add(1)
			return &ownedResource{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	component := owned.Component()
	if err := component.Stop(context.Background()); err != nil {
		t.Fatalf("stop before start = %v", err)
	}
	if err := component.Start(context.Background()); !errors.Is(err, service.ErrResourceClosed) || opens.Load() != 0 {
		t.Fatalf("start after stop = %v opens = %d", err, opens.Load())
	}
	if _, err := owned.Current(); !errors.Is(err, service.ErrResourceClosed) {
		t.Fatalf("current = %v", err)
	}
}

func TestOwnedCloseFailureAndTimeoutAreRetained(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		closeErr := errors.New("close failed")
		resource := &ownedResource{close: func(ctx context.Context) error {
			<-ctx.Done()
			return errors.Join(closeErr, ctx.Err())
		}}
		owned, err := service.NewOwned(service.OwnedConfig[*ownedResource]{
			Name: "resource", CloseTimeout: 2 * time.Second,
			Open: func(context.Context) (*ownedResource, error) { return resource, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		component := owned.Component()
		if err := component.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		began := time.Now()
		results := make(chan error, 4)
		for range 4 {
			go func() { results <- component.Stop(context.Background()) }()
		}
		for range 4 {
			err := <-results
			if !errors.Is(err, closeErr) || !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("stop = %v, want close failure and deadline", err)
			}
		}
		if elapsed := time.Since(began); elapsed != 2*time.Second {
			t.Errorf("close lasted %v, want CloseTimeout", elapsed)
		}
		if resource.closes.Load() != 1 {
			t.Fatalf("closes = %d", resource.closes.Load())
		}
		if err := component.Stop(context.Background()); !errors.Is(err, closeErr) {
			t.Fatalf("repeated stop = %v, want retained failure", err)
		}
		// A waiter's own context bounds only its wait, not the close.
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := component.Stop(ctx); !errors.Is(err, closeErr) {
			t.Fatalf("completed stop with canceled waiter = %v", err)
		}
	})
}

func TestOwnedCloseWaiterContextBoundsOnlyItsWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		resource := &ownedResource{close: func(context.Context) error { <-release; return nil }}
		owned, err := service.NewOwned(service.OwnedConfig[*ownedResource]{
			Name: "resource", Open: func(context.Context) (*ownedResource, error) { return resource, nil },
		})
		if err != nil {
			t.Fatal(err)
		}
		component := owned.Component()
		if err := component.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		first := make(chan error, 1)
		go func() { first <- component.Stop(context.Background()) }()
		synctest.Wait()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := component.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("bounded waiter = %v", err)
		}
		close(release)
		if err := <-first; err != nil {
			t.Fatalf("first stop = %v", err)
		}
		if resource.closes.Load() != 1 {
			t.Fatalf("closes = %d", resource.closes.Load())
		}
	})
}

func TestOwnedStopDuringOpenDoesNotLeak(t *testing.T) {
	for _, cooperative := range []bool{true, false} {
		t.Run(fmt.Sprintf("cooperative=%t", cooperative), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				resource := &ownedResource{}
				var discardDeadline time.Time
				resource.close = func(ctx context.Context) error {
					discardDeadline, _ = ctx.Deadline()
					return ctx.Err()
				}
				var openCause error
				owned, err := service.NewOwned(service.OwnedConfig[*ownedResource]{
					Name: "resource", CloseTimeout: 5 * time.Second,
					Open: func(ctx context.Context) (*ownedResource, error) {
						close(entered)
						if cooperative {
							<-ctx.Done()
							openCause = context.Cause(ctx)
							return nil, context.Cause(ctx)
						}
						<-release
						return resource, nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				component := owned.Component()
				started := make(chan error, 1)
				go func() { started <- component.Start(context.Background()) }()
				<-entered
				stopped := make(chan error, 1)
				go func() { stopped <- component.Stop(context.Background()) }()
				synctest.Wait()
				if _, err := owned.Current(); !errors.Is(err, service.ErrResourceClosed) {
					t.Fatalf("current during stop = %v", err)
				}
				if !cooperative {
					select {
					case err := <-stopped:
						t.Fatalf("stop returned before the in-flight open was discarded: %v", err)
					default:
					}
					close(release)
				}
				if err := <-stopped; err != nil {
					t.Fatalf("stop = %v", err)
				}
				startErr := <-started
				if !errors.Is(startErr, service.ErrResourceClosed) {
					t.Fatalf("start = %v, want ErrResourceClosed", startErr)
				}
				if cooperative {
					if !errors.Is(openCause, service.ErrResourceClosed) || resource.closes.Load() != 0 {
						t.Fatalf("open cause = %v closes = %d", openCause, resource.closes.Load())
					}
					return
				}
				if resource.closes.Load() != 1 || discardDeadline.IsZero() {
					t.Fatalf("discarded resource closes = %d deadline = %v", resource.closes.Load(), discardDeadline)
				}
				if _, err := owned.Current(); !errors.Is(err, service.ErrResourceClosed) {
					t.Fatalf("current after stop = %v", err)
				}
			})
		})
	}
}

func TestOwnedOpenSucceedingAfterCancellationIsClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		closeErr := errors.New("discard failed")
		resource := &ownedResource{}
		var discardErr error
		resource.close = func(ctx context.Context) error {
			discardErr = ctx.Err()
			return closeErr
		}
		cause := errors.New("startup abandoned")
		ctx, cancel := context.WithCancelCause(context.Background())
		owned, err := service.NewOwned(service.OwnedConfig[*ownedResource]{
			Name: "resource",
			Open: func(context.Context) (*ownedResource, error) {
				cancel(cause)
				return resource, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		err = owned.Component().Start(ctx)
		if !errors.Is(err, cause) || !errors.Is(err, closeErr) {
			t.Fatalf("start = %v, want cancellation cause and close failure", err)
		}
		if resource.closes.Load() != 1 || discardErr != nil {
			t.Fatalf("closes = %d, discard context error = %v", resource.closes.Load(), discardErr)
		}
		if _, err := owned.Current(); !errors.Is(err, service.ErrResourceNotStarted) {
			t.Fatalf("current = %v", err)
		}
		canceled, cancelAgain := context.WithCancel(context.Background())
		cancelAgain()
		if err := owned.Component().Start(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("start with ended context = %v", err)
		}
	})
}

func TestOwnedConcurrentLifecycleClosesEveryOpenedResourceOnce(t *testing.T) {
	for range 50 {
		var mu sync.Mutex
		var opened []*ownedResource
		owned, err := service.NewOwned(service.OwnedConfig[*ownedResource]{
			Name: "resource",
			Open: func(ctx context.Context) (*ownedResource, error) {
				resource := &ownedResource{}
				mu.Lock()
				opened = append(opened, resource)
				mu.Unlock()
				return resource, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		component := owned.Component()
		var callers sync.WaitGroup
		for index := range 24 {
			callers.Go(func() {
				switch index % 3 {
				case 0:
					err := component.Start(context.Background())
					if err != nil && !errors.Is(err, service.ErrResourceAlreadyStarted) && !errors.Is(err, service.ErrResourceClosed) {
						t.Errorf("start = %v", err)
					}
				case 1:
					if err := component.Stop(context.Background()); err != nil {
						t.Errorf("stop = %v", err)
					}
				default:
					current, err := owned.Current()
					if err == nil && current == nil {
						t.Error("current returned a nil resource without an error")
					}
				}
			})
		}
		callers.Wait()
		if _, err := owned.Current(); !errors.Is(err, service.ErrResourceClosed) {
			t.Fatalf("current after concurrent stops = %v", err)
		}
		if len(opened) > 1 {
			t.Fatalf("opened %d resources, want at most one", len(opened))
		}
		for _, resource := range opened {
			if resource.closes.Load() != 1 {
				t.Fatalf("opened resource closed %d times", resource.closes.Load())
			}
		}
	}
}

func TestOwnedClosePanicBecomesPanicError(t *testing.T) {
	owned, err := service.NewOwned(service.OwnedConfig[int]{
		Name:  "resource",
		Open:  func(context.Context) (int, error) { return 1, nil },
		Close: func(context.Context, int) error { panic("close exploded") },
	})
	if err != nil {
		t.Fatal(err)
	}
	component := owned.Component()
	if err := component.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var panicError *service.PanicError
	if err := component.Stop(context.Background()); !errors.As(err, &panicError) || panicError.Operation != "close" {
		t.Fatalf("stop = %v", err)
	}
	if err := component.Stop(context.Background()); !errors.As(err, &panicError) {
		t.Fatalf("repeated stop = %v", err)
	}
}

var errClientNotStarted = errors.New("client: not started")

func ExampleOwned() {
	owned, err := service.NewOwned(service.OwnedConfig[*ownedResource]{
		Name:         "client",
		Open:         func(context.Context) (*ownedResource, error) { return &ownedResource{id: 1}, nil },
		CloseTimeout: 10 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	current := func() (*ownedResource, error) {
		resource, err := owned.Current()
		if errors.Is(err, service.ErrResourceNotStarted) {
			// Applications keep their own sentinel while preserving the library's.
			return nil, fmt.Errorf("%w: %w", errClientNotStarted, err)
		}
		return resource, err
	}
	_, err = current()
	fmt.Println(errors.Is(err, errClientNotStarted), errors.Is(err, service.ErrResourceNotStarted))

	component := owned.Component()
	_ = component.Start(context.Background())
	resource, _ := current()
	fmt.Println(resource.id)
	_ = component.Stop(context.Background())
	_, err = current()
	fmt.Println(errors.Is(err, service.ErrResourceClosed))
	// Output:
	// true true
	// 1
	// true
}
