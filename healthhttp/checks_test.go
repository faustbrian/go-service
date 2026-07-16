package healthhttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/faustbrian/go-service/healthhttp"
)

func TestConcurrentChecksRespectBoundsAndRegistrationOrder(t *testing.T) {
	t.Parallel()

	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	release := make(chan struct{})
	probes, err := healthhttp.New(healthhttp.Config{
		Mode:           healthhttp.ModeConcurrent,
		MaxConcurrency: 2,
		CheckTimeout:   time.Second,
		Details:        true,
		Checks: []healthhttp.Check{
			{Name: "first", Run: blockingCheck(firstEntered, release)},
			{Name: "second", Run: blockingCheck(secondEntered, release)},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result := make(chan healthhttp.Response, 1)
	go func() { result <- serveProbe(t, probes.Readiness()) }()
	<-firstEntered
	<-secondEntered
	close(release)
	response := <-result
	want := []healthhttp.CheckResult{
		{Name: "first", Status: "ok"},
		{Name: "second", Status: "ok"},
	}
	if !reflect.DeepEqual(response.Checks, want) {
		t.Fatalf("checks = %#v, want %#v", response.Checks, want)
	}
}

func TestSequentialChecksDoNotOverlap(t *testing.T) {
	t.Parallel()

	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	releaseSecond := make(chan struct{})
	probes, err := healthhttp.New(healthhttp.Config{
		Mode:         healthhttp.ModeSequential,
		CheckTimeout: time.Second,
		Checks: []healthhttp.Check{
			{Name: "first", Run: blockingCheck(firstEntered, releaseFirst)},
			{Name: "second", Run: blockingCheck(secondEntered, releaseSecond)},
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	done := make(chan struct{})
	go func() {
		serveProbe(t, probes.Readiness())
		close(done)
	}()
	<-firstEntered
	select {
	case <-secondEntered:
		t.Fatal("second check overlapped first")
	default:
	}
	close(releaseFirst)
	<-secondEntered
	close(releaseSecond)
	<-done
}

func TestConcurrentChecksQueueWithinConcurrencyBound(t *testing.T) {
	t.Parallel()

	firstEntered := make(chan struct{})
	release := make(chan struct{})
	var enteredOnce sync.Once
	var calls atomic.Int32
	checks := make([]healthhttp.Check, 3)
	for index := range checks {
		checks[index] = healthhttp.Check{
			Name: string(rune('a' + index)),
			Run: func(context.Context) error {
				calls.Add(1)
				enteredOnce.Do(func() { close(firstEntered) })
				<-release

				return nil
			},
		}
	}
	probes, err := healthhttp.New(healthhttp.Config{
		Checks:         checks,
		MaxConcurrency: 1,
		CheckTimeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result := make(chan healthhttp.Response, 1)
	go func() { result <- serveProbe(t, probes.Readiness()) }()
	<-firstEntered
	close(release)
	if response := <-result; response.Status != "ok" {
		t.Fatalf("status = %q, want ok", response.Status)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("check calls = %d, want 3", got)
	}
}

func TestIgnoringAndPanickingChecksAreBoundedAndRedacted(t *testing.T) {
	t.Parallel()

	invocations := make(chan struct{}, 2)
	var invocationCount atomic.Int32
	release := make(chan struct{})
	probes, err := healthhttp.New(healthhttp.Config{
		Mode:           healthhttp.ModeConcurrent,
		MaxConcurrency: 1,
		CheckTimeout:   time.Second,
		Details:        true,
		Checks: []healthhttp.Check{{
			Name: "stuck",
			Run: func(context.Context) error {
				invocationCount.Add(1)
				invocations <- struct{}{}
				<-release

				return nil
			},
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	requestContext, cancelRequest := context.WithCancel(context.Background())
	firstResult := make(chan healthhttp.Response, 1)
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		firstResult <- serveProbeRequest(t, probes.Readiness(), request.WithContext(requestContext))
	}()
	<-invocations
	cancelRequest()
	if response := <-firstResult; response.Status != "unavailable" {
		t.Fatalf("first status = %q, want unavailable", response.Status)
	}
	secondContext, cancelSecond := context.WithCancel(context.Background())
	cancelSecond()
	secondRequest := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(secondContext)
	if response := serveProbeRequest(t, probes.Readiness(), secondRequest); response.Status != "unavailable" {
		t.Fatalf("second status = %q, want unavailable", response.Status)
	}
	if calls := invocationCount.Load(); calls != 1 {
		t.Fatalf("check invocations = %d, want globally bounded at 1", calls)
	}
	close(release)

	panicProbes, err := healthhttp.New(healthhttp.Config{
		Details: true,
		Checks: []healthhttp.Check{{
			Name: "panicking",
			Run: func(context.Context) error {
				panic("secret panic")
			},
		}},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response := serveProbe(t, panicProbes.Readiness())
	want := []healthhttp.CheckResult{{Name: "panicking", Status: "unavailable"}}
	if !reflect.DeepEqual(response.Checks, want) {
		t.Fatalf("checks = %#v, want %#v", response.Checks, want)
	}
}

func TestNewRejectsInvalidHealthConfiguration(t *testing.T) {
	t.Parallel()

	tests := map[string]healthhttp.Config{
		"invalid mode":          {Mode: healthhttp.Mode(255)},
		"negative timeout":      {CheckTimeout: -1},
		"negative concurrency":  {MaxConcurrency: -1},
		"negative maximum":      {MaxChecks: -1},
		"excessive maximum":     {MaxChecks: 1025},
		"excessive concurrency": {MaxConcurrency: 257},
		"concurrency above maximum": {
			MaxConcurrency: 2,
			MaxChecks:      1,
		},
		"blank name": {Checks: []healthhttp.Check{{Run: func(context.Context) error { return nil }}}},
		"nil check":  {Checks: []healthhttp.Check{{Name: "nil"}}},
		"duplicate": {Checks: []healthhttp.Check{
			{Name: "same", Run: func(context.Context) error { return nil }},
			{Name: "same", Run: func(context.Context) error { return nil }},
		}},
		"too many": {
			MaxChecks:      1,
			MaxConcurrency: 1,
			Checks: []healthhttp.Check{
				{Name: "one", Run: func(context.Context) error { return nil }},
				{Name: "two", Run: func(context.Context) error { return nil }},
			},
		},
	}
	for name, config := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := healthhttp.New(config)
			if !errors.Is(err, healthhttp.ErrInvalidConfig) {
				t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func blockingCheck(entered chan<- struct{}, release <-chan struct{}) healthhttp.CheckFunc {
	return func(context.Context) error {
		close(entered)
		<-release

		return nil
	}
}

func serveProbe(t *testing.T, handler http.Handler) healthhttp.Response {
	t.Helper()

	return serveProbeRequest(t, handler, httptest.NewRequest(http.MethodGet, "/", nil))
}

func serveProbeRequest(
	t *testing.T,
	handler http.Handler,
	request *http.Request,
) healthhttp.Response {
	t.Helper()

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	var response healthhttp.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("response JSON error = %v", err)
	}

	return response
}
