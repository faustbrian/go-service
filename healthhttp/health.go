// Package healthhttp provides stable HTTP liveness, startup, and readiness
// probes with bounded dependency checks.
package healthhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	defaultCheckTimeout   = time.Second
	defaultMaxConcurrency = 4
	defaultMaxChecks      = 64
	maximumConcurrency    = 256
	maximumChecks         = 1024
)

// ErrInvalidConfig identifies invalid probe configuration.
var ErrInvalidConfig = errors.New("invalid health probe configuration")

// ConfigError identifies one invalid probe field.
type ConfigError struct {
	// Field identifies the rejected configuration path.
	Field string
	// Reason describes why Field was rejected.
	Reason string
}

// Error implements error.
func (err *ConfigError) Error() string {
	return fmt.Sprintf("%s: %s: %v", err.Field, err.Reason, ErrInvalidConfig)
}

// Unwrap makes ConfigError inspectable with errors.Is.
func (err *ConfigError) Unwrap() error {
	return ErrInvalidConfig
}

// StateSource is the lifecycle surface required by probes.
type StateSource interface {
	// StartupComplete reports whether required startup completed successfully.
	StartupComplete() bool
	// Ready reports whether the process should accept new work.
	Ready() bool
}

// CheckFunc evaluates one readiness dependency.
type CheckFunc func(context.Context) error

// Check is one named readiness dependency check.
type Check struct {
	// Name is the stable secret-safe diagnostic name.
	Name string
	// Run evaluates the dependency and must honor context cancellation.
	Run CheckFunc
}

// Mode controls dependency check scheduling.
type Mode uint8

const (
	// ModeConcurrent schedules checks concurrently within MaxConcurrency.
	ModeConcurrent Mode = iota
	// ModeSequential evaluates checks in registration order without overlap.
	ModeSequential
)

// Config describes probe lifecycle, dependency checks, and resource bounds.
type Config struct {
	// Lifecycle optionally gates startup and readiness by service state.
	Lifecycle StateSource
	// Checks are readiness dependencies evaluated in registration order.
	Checks []Check
	// Mode selects concurrent or sequential evaluation.
	Mode Mode
	// CheckTimeout bounds each check, including its wait for a concurrency slot.
	// Zero uses the documented default.
	CheckTimeout time.Duration
	// MaxConcurrency caps checks active across all requests. Zero uses the
	// documented default.
	MaxConcurrency int
	// MaxChecks caps registered checks and per-request scheduling. Zero uses the
	// documented default.
	MaxChecks int
	// Details includes only check names and binary statuses in responses.
	Details bool
	// Observer receives bounded probe results. Nil disables observation.
	Observer Observer
}

// CheckResult is a secret-safe dependency status.
type CheckResult struct {
	// Name is the configured secret-safe check name.
	Name string `json:"name"`
	// Status is either ok or unavailable.
	Status string `json:"status"`
}

// Response is the stable machine-readable probe response.
type Response struct {
	// Status is either ok or unavailable.
	Status string `json:"status"`
	// Probe is liveness, startup, or readiness.
	Probe string `json:"probe"`
	// Checks is omitted unless detailed diagnostics are enabled.
	Checks []CheckResult `json:"checks,omitempty"`
}

// Observation is one bounded management-probe result.
type Observation struct {
	// Probe is liveness, startup, or readiness.
	Probe string
	// Available is the status returned to the caller.
	Available bool
	// Duration is the complete probe evaluation duration.
	Duration time.Duration
}

// Observer receives management-probe results. Implementations must handle
// concurrent calls and return promptly. Observer panics are contained and do
// not alter probe responses.
type Observer interface {
	// ObserveProbe receives one completed probe result.
	ObserveProbe(context.Context, Observation)
}

// ObserverFunc adapts a function to Observer.
type ObserverFunc func(context.Context, Observation)

// ObserveProbe invokes the adapted observer.
func (observe ObserverFunc) ObserveProbe(ctx context.Context, observation Observation) {
	observe(ctx, observation)
}

// Probes contains independently mountable probe handlers.
type Probes struct {
	lifecycle StateSource
	checks    []Check
	mode      Mode
	timeout   time.Duration
	details   bool
	semaphore chan struct{}
	observer  Observer
}

// New validates and constructs probe handlers without starting goroutines.
func New(config Config) (*Probes, error) {
	if config.Mode != ModeConcurrent && config.Mode != ModeSequential {
		return nil, &ConfigError{Field: "Mode", Reason: "is unknown"}
	}
	if config.CheckTimeout < 0 {
		return nil, &ConfigError{Field: "CheckTimeout", Reason: "must not be negative"}
	}
	if config.MaxConcurrency < 0 {
		return nil, &ConfigError{Field: "MaxConcurrency", Reason: "must not be negative"}
	}
	if config.MaxChecks < 0 {
		return nil, &ConfigError{Field: "MaxChecks", Reason: "must not be negative"}
	}
	if config.MaxConcurrency > maximumConcurrency {
		return nil, &ConfigError{Field: "MaxConcurrency", Reason: "exceeds hard limit"}
	}
	if config.MaxChecks > maximumChecks {
		return nil, &ConfigError{Field: "MaxChecks", Reason: "exceeds hard limit"}
	}

	timeout := config.CheckTimeout
	if timeout == 0 {
		timeout = defaultCheckTimeout
	}
	maxConcurrency := config.MaxConcurrency
	switch maxConcurrency {
	case 0:
		maxConcurrency = defaultMaxConcurrency
	}
	maxChecks := config.MaxChecks
	if maxChecks == 0 {
		maxChecks = defaultMaxChecks
	}
	if maxConcurrency > maxChecks {
		return nil, &ConfigError{
			Field:  "MaxConcurrency",
			Reason: "must not exceed MaxChecks",
		}
	}
	if len(config.Checks) > maxChecks {
		return nil, &ConfigError{Field: "Checks", Reason: "exceeds MaxChecks"}
	}

	names := make(map[string]struct{}, len(config.Checks))
	for index, check := range config.Checks {
		if strings.TrimSpace(check.Name) == "" {
			return nil, &ConfigError{
				Field:  fmt.Sprintf("Checks[%d].Name", index),
				Reason: "must not be blank",
			}
		}
		if check.Run == nil {
			return nil, &ConfigError{
				Field:  fmt.Sprintf("Checks[%d].Run", index),
				Reason: "must not be nil",
			}
		}
		if _, duplicate := names[check.Name]; duplicate {
			return nil, &ConfigError{
				Field:  fmt.Sprintf("Checks[%d].Name", index),
				Reason: "must be unique",
			}
		}
		names[check.Name] = struct{}{}
	}

	return &Probes{
		lifecycle: config.Lifecycle,
		checks:    append([]Check(nil), config.Checks...),
		mode:      config.Mode,
		timeout:   timeout,
		details:   config.Details,
		semaphore: make(chan struct{}, maxConcurrency),
		observer:  config.Observer,
	}, nil
}

// Liveness returns a handler that reports whether the process can serve HTTP.
func (probes *Probes) Liveness() http.Handler {
	return probes.handler("liveness", func(*http.Request) (bool, []CheckResult) {
		return true, nil
	})
}

// Startup returns a handler that succeeds after lifecycle startup completes.
func (probes *Probes) Startup() http.Handler {
	return probes.handler("startup", func(*http.Request) (bool, []CheckResult) {
		if probes.lifecycle == nil {
			return true, nil
		}

		return probes.lifecycle.StartupComplete(), nil
	})
}

// Readiness returns a handler that requires a ready lifecycle and successful
// dependency checks.
func (probes *Probes) Readiness() http.Handler {
	return probes.handler("readiness", func(request *http.Request) (bool, []CheckResult) {
		if probes.lifecycle != nil && !probes.lifecycle.Ready() {
			return false, nil
		}

		return probes.evaluate(request.Context())
	})
}

func (probes *Probes) handler(
	probe string,
	healthy func(*http.Request) (bool, []CheckResult),
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			writer.Header().Set("Allow", "GET, HEAD")
			writer.WriteHeader(http.StatusMethodNotAllowed)

			return
		}

		started := time.Now()
		available, checks := healthy(request)
		probes.observe(request.Context(), Observation{
			Probe: probe, Available: available, Duration: time.Since(started),
		})
		status := "ok"
		statusCode := http.StatusOK
		if !available {
			status = "unavailable"
			statusCode = http.StatusServiceUnavailable
		}
		if !probes.details {
			checks = nil
		}

		writer.WriteHeader(statusCode)
		if request.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(writer).Encode(Response{
			Status: status,
			Probe:  probe,
			Checks: checks,
		})
	})
}

func (probes *Probes) observe(ctx context.Context, observation Observation) {
	if probes.observer == nil {
		return
	}
	defer func() { _ = recover() }()
	probes.observer.ObserveProbe(ctx, observation)
}

func (probes *Probes) evaluate(ctx context.Context) (bool, []CheckResult) {
	results := make([]CheckResult, len(probes.checks))
	if probes.mode == ModeSequential {
		available := true
		for index, check := range probes.checks {
			results[index] = probes.evaluateCheck(ctx, check)
			available = available && results[index].Status == "ok"
		}

		return available, results
	}

	type indexedResult struct {
		index  int
		result CheckResult
	}
	completed := make(chan indexedResult, len(probes.checks))
	deadline := time.Now().Add(probes.timeout)
	started := 0
	for index, check := range probes.checks {
		results[index] = CheckResult{Name: check.Name, Status: "unavailable"}
		checkContext, cancel := context.WithDeadline(ctx, deadline)
		select {
		case probes.semaphore <- struct{}{}:
		case <-checkContext.Done():
			cancel()

			continue
		}
		started++
		go func() {
			result := probes.evaluateAcquiredCheck(checkContext, check)
			cancel()
			completed <- indexedResult{
				index:  index,
				result: result,
			}
		}()
	}
	available := true
	for range started {
		item := <-completed
		results[item.index] = item.result
	}
	for _, result := range results {
		available = available && result.Status == "ok"
	}

	return available, results
}

func (probes *Probes) evaluateCheck(ctx context.Context, check Check) CheckResult {
	result := CheckResult{Name: check.Name, Status: "unavailable"}
	checkContext, cancel := context.WithTimeout(ctx, probes.timeout)
	defer cancel()

	select {
	case probes.semaphore <- struct{}{}:
	case <-checkContext.Done():
		return result
	}

	return probes.evaluateAcquiredCheck(checkContext, check)
}

func (probes *Probes) evaluateAcquiredCheck(
	ctx context.Context,
	check Check,
) CheckResult {
	result := CheckResult{Name: check.Name, Status: "unavailable"}
	completed := make(chan bool, 1)
	go func() {
		defer func() { <-probes.semaphore }()
		completed <- runCheck(ctx, check.Run)
	}()
	select {
	case succeeded := <-completed:
		if succeeded {
			result.Status = "ok"
		}
	case <-ctx.Done():
	}

	return result
}

func runCheck(ctx context.Context, check CheckFunc) (succeeded bool) {
	defer func() {
		if recover() != nil {
			succeeded = false
		}
	}()

	return check(ctx) == nil
}
