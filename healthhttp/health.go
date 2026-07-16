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

	"github.com/faustbrian/go-service/service"
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
	Field  string
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
	State() service.State
}

// CheckFunc evaluates one readiness dependency.
type CheckFunc func(context.Context) error

// Check is one named readiness dependency check.
type Check struct {
	Name string
	Run  CheckFunc
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
	Lifecycle      StateSource
	Checks         []Check
	Mode           Mode
	CheckTimeout   time.Duration
	MaxConcurrency int
	MaxChecks      int
	Details        bool
}

// CheckResult is a secret-safe dependency status.
type CheckResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Response is the stable machine-readable probe response.
type Response struct {
	Status string        `json:"status"`
	Probe  string        `json:"probe"`
	Checks []CheckResult `json:"checks,omitempty"`
}

// Probes contains independently mountable probe handlers.
type Probes struct {
	lifecycle StateSource
	checks    []Check
	mode      Mode
	timeout   time.Duration
	details   bool
	semaphore chan struct{}
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
	if maxConcurrency == 0 {
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

		switch probes.lifecycle.State() {
		case service.StateReady,
			service.StateDraining,
			service.StateStopping,
			service.StateStopped:
			return true, nil
		default:
			return false, nil
		}
	})
}

// Readiness returns a handler that requires a ready lifecycle and successful
// dependency checks.
func (probes *Probes) Readiness() http.Handler {
	return probes.handler("readiness", func(request *http.Request) (bool, []CheckResult) {
		if probes.lifecycle != nil && probes.lifecycle.State() != service.StateReady {
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
		available, checks := healthy(request)
		status := "ok"
		statusCode := http.StatusOK
		if !available {
			status = "unavailable"
			statusCode = http.StatusServiceUnavailable
		}
		if !probes.details {
			checks = nil
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(statusCode)
		_ = json.NewEncoder(writer).Encode(Response{
			Status: status,
			Probe:  probe,
			Checks: checks,
		})
	})
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
	for index, check := range probes.checks {
		go func() {
			completed <- indexedResult{index: index, result: probes.evaluateCheck(ctx, check)}
		}()
	}
	available := true
	for range probes.checks {
		item := <-completed
		results[item.index] = item.result
		available = available && item.result.Status == "ok"
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

	completed := make(chan bool, 1)
	go func() {
		defer func() { <-probes.semaphore }()
		completed <- runCheck(checkContext, check.Run)
	}()
	select {
	case succeeded := <-completed:
		if succeeded {
			result.Status = "ok"
		}
	case <-checkContext.Done():
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
