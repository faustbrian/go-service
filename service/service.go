// Package service coordinates ordered service startup and shutdown.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	// ErrShutdown is the cancellation cause used for an explicit shutdown.
	ErrShutdown = errors.New("service shutdown")
	// ErrInvalidConfig identifies configuration rejected before startup.
	ErrInvalidConfig = errors.New("invalid service configuration")
	// ErrInvalidState identifies an operation rejected by the state machine.
	ErrInvalidState = errors.New("invalid service state")
)

const (
	defaultRollbackTimeout = 30 * time.Second
	defaultMaxTasks        = 64
	maximumTasks           = 4096
)

// State is a service lifecycle state.
type State uint8

const (
	// StateNew is the initial state before startup begins.
	StateNew State = iota
	// StateStarting means components are starting.
	StateStarting
	// StateReady means every component started successfully.
	StateReady
	// StateDraining means new work should no longer be accepted.
	StateDraining
	// StateStopping means owned components are stopping.
	StateStopping
	// StateStopped is the terminal lifecycle state.
	StateStopped
)

// String returns the stable machine-readable state name.
func (state State) String() string {
	switch state {
	case StateNew:
		return "new"
	case StateStarting:
		return "starting"
	case StateReady:
		return "ready"
	case StateDraining:
		return "draining"
	case StateStopping:
		return "stopping"
	case StateStopped:
		return "stopped"
	default:
		return fmt.Sprintf("state(%d)", state)
	}
}

// Component is one named unit of ordered lifecycle work.
type Component struct {
	// Name is the unique diagnostic name used in typed lifecycle errors.
	Name string
	// Start acquires the component resources and transfers ownership only when
	// it returns nil. It must honor context cancellation.
	Start func(context.Context) error
	// Stop releases resources after a successful Start and must honor context
	// cancellation. Nil is a valid no-op.
	Stop func(context.Context) error
}

// Config describes a Service.
type Config struct {
	// Components start in listed order and stop in reverse successful order.
	Components []Component
	// RollbackTimeout bounds the caller's wait after startup failure. Zero uses
	// the documented default.
	RollbackTimeout time.Duration
	// MaxTasks caps concurrently active supervised tasks. Zero uses the
	// documented default.
	MaxTasks int
}

// ConfigError identifies one invalid configuration field.
type ConfigError struct {
	// Field identifies the rejected configuration path.
	Field string
	// Reason describes why Field was rejected without exposing secret values.
	Reason string
}

// StateError reports an operation that is invalid in the current state.
type StateError struct {
	// Operation is the rejected lifecycle operation.
	Operation string
	// State is the lifecycle state in which Operation was rejected.
	State State
}

// Error implements error.
func (err *StateError) Error() string {
	return fmt.Sprintf("cannot %s service in %s state: %v",
		err.Operation, err.State, ErrInvalidState)
}

// Unwrap makes StateError inspectable with errors.Is.
func (err *StateError) Unwrap() error {
	return ErrInvalidState
}

// ComponentError identifies a failed component operation.
type ComponentError struct {
	// Component is the configured component or supervised task name.
	Component string
	// Operation is the failed lifecycle operation.
	Operation string
	// Err is the original failure and remains available through errors.Is and
	// errors.As.
	Err error
}

// PanicError reports a recovered component panic without formatting its value
// into an error string that may be logged or returned externally.
type PanicError struct {
	// Component is the component or supervised task that panicked.
	Component string
	// Operation is the lifecycle operation that panicked.
	Operation string
	// Value is the recovered panic value. Error deliberately does not format it.
	Value any
}

// Error implements error without disclosing the recovered value.
func (err *PanicError) Error() string {
	return fmt.Sprintf("%s component %q panicked", err.Operation, err.Component)
}

// Error implements error.
func (err *ComponentError) Error() string {
	return fmt.Sprintf("%s component %q: %v", err.Operation, err.Component, err.Err)
}

// Unwrap returns the underlying component failure.
func (err *ComponentError) Unwrap() error {
	return err.Err
}

// StartupError reports a component start failure and every rollback failure.
type StartupError struct {
	// Component identifies the component whose startup failed.
	Component string
	// Err is the startup failure.
	Err error
	// Rollback contains every retained reverse-cleanup failure.
	Rollback []error
}

// ShutdownError aggregates every component and supervised-task failure
// observed while stopping a service.
type ShutdownError struct {
	// Failures contains component and supervised-task failures in observation
	// order.
	Failures []error
}

// Error implements error without flattening failure details into one string.
func (err *ShutdownError) Error() string {
	return fmt.Sprintf("service shutdown failed with %d error(s)", len(err.Failures))
}

// Unwrap exposes every shutdown failure to errors.Is and errors.As.
func (err *ShutdownError) Unwrap() []error {
	return err.Failures
}

// Error implements error.
func (err *StartupError) Error() string {
	if len(err.Rollback) == 0 {
		return fmt.Sprintf("start component %q: %v", err.Component, err.Err)
	}

	return fmt.Sprintf("start component %q: %v; rollback: %v",
		err.Component, err.Err, errors.Join(err.Rollback...))
}

// Unwrap exposes the startup and rollback failures to errors.Is and errors.As.
func (err *StartupError) Unwrap() []error {
	causes := make([]error, 0, len(err.Rollback)+1)
	causes = append(causes, err.Err)

	return append(causes, err.Rollback...)
}

// Error implements error.
func (err *ConfigError) Error() string {
	return fmt.Sprintf("%s: %s: %v", err.Field, err.Reason, ErrInvalidConfig)
}

// Unwrap makes ConfigError inspectable with errors.Is.
func (err *ConfigError) Unwrap() error {
	return ErrInvalidConfig
}

// Service coordinates the components it owns.
type Service struct {
	mu           sync.RWMutex
	state        State
	components   []Component
	started      int
	ctx          context.Context
	cancel       context.CancelCauseFunc
	rollback     time.Duration
	shutdownErr  error
	shutdownDone chan struct{}
	startDone    chan struct{}
	taskCount    int
	taskErrors   []error
	stopErrors   []error
	stopsDone    bool
	maxTasks     int
}

// New constructs a service without starting background work.
func New(config Config) (*Service, error) {
	if config.RollbackTimeout < 0 {
		return nil, &ConfigError{
			Field:  "RollbackTimeout",
			Reason: "must not be negative",
		}
	}
	if config.MaxTasks < 0 || config.MaxTasks > maximumTasks {
		return nil, &ConfigError{
			Field:  "MaxTasks",
			Reason: "is outside the allowed range",
		}
	}

	names := make(map[string]struct{}, len(config.Components))
	for index, component := range config.Components {
		if strings.TrimSpace(component.Name) == "" {
			return nil, &ConfigError{
				Field:  fmt.Sprintf("Components[%d].Name", index),
				Reason: "must not be blank",
			}
		}
		if _, exists := names[component.Name]; exists {
			return nil, &ConfigError{
				Field:  fmt.Sprintf("Components[%d].Name", index),
				Reason: fmt.Sprintf("duplicates %q", component.Name),
			}
		}

		names[component.Name] = struct{}{}
	}

	rollback := config.RollbackTimeout
	if rollback == 0 {
		rollback = defaultRollbackTimeout
	}
	maxTasks := config.MaxTasks
	if maxTasks == 0 {
		maxTasks = defaultMaxTasks
	}

	return &Service{
		components: append([]Component(nil), config.Components...),
		rollback:   rollback,
		maxTasks:   maxTasks,
	}, nil
}

// State returns the current lifecycle state.
func (service *Service) State() State {
	service.mu.RLock()
	defer service.mu.RUnlock()

	return service.state
}

// Context returns the service-owned context, or context.Background before
// startup.
func (service *Service) Context() context.Context {
	service.mu.RLock()
	defer service.mu.RUnlock()

	if service.ctx == nil {
		return context.Background()
	}

	return service.ctx
}

// Start starts components in registration order.
func (service *Service) Start(parent context.Context) error {
	if parent == nil {
		return &ConfigError{Field: "parent", Reason: "must not be nil"}
	}

	service.mu.Lock()
	if service.state != StateNew {
		state := service.state
		service.mu.Unlock()

		return &StateError{Operation: "start", State: state}
	}

	service.state = StateStarting
	service.ctx, service.cancel = context.WithCancelCause(parent)
	service.startDone = make(chan struct{})
	service.mu.Unlock()

	for index, component := range service.components {
		if component.Start == nil {
			service.mu.Lock()
			service.started = index + 1
			cause := context.Cause(service.ctx)
			service.mu.Unlock()
			if cause != nil {
				return service.failStartup("service", cause)
			}

			continue
		}

		if err := invoke(component.Name, "start", component.Start, service.ctx); err != nil {
			return service.failStartup(component.Name, err)
		}

		service.mu.Lock()
		service.started = index + 1
		cause := context.Cause(service.ctx)
		service.mu.Unlock()
		if cause != nil {
			return service.failStartup("service", cause)
		}
	}

	service.mu.Lock()
	if cause := context.Cause(service.ctx); cause != nil {
		service.mu.Unlock()

		return service.failStartup("service", cause)
	}
	service.state = StateReady
	close(service.startDone)
	service.mu.Unlock()

	return nil
}

// Ready reports whether every component is started and the service is still
// accepting new work.
func (service *Service) Ready() bool {
	return service.State() == StateReady
}

// Drain marks a ready service as unavailable for new work.
func (service *Service) Drain() error {
	service.mu.Lock()
	defer service.mu.Unlock()

	switch service.state {
	case StateReady:
		service.state = StateDraining

		return nil
	case StateDraining:
		return nil
	default:
		return &StateError{Operation: "drain", State: service.state}
	}
}

// Go starts one named supervised task. The task receives the service context
// and must return after cancellation. An error or panic cancels the service and
// moves a ready service to draining. An error matching the canceled task
// context or its cause is a normal shutdown result. Shutdown joins every
// supervised task.
func (service *Service) Go(
	name string,
	task func(context.Context) error,
) error {
	if strings.TrimSpace(name) == "" {
		return &ConfigError{Field: "name", Reason: "must not be blank"}
	}
	if task == nil {
		return &ConfigError{Field: "task", Reason: "must not be nil"}
	}

	service.mu.Lock()
	if service.state != StateReady {
		state := service.state
		service.mu.Unlock()

		return &StateError{Operation: "start supervised task", State: state}
	}
	maxTasks := service.maxTasks
	if maxTasks == 0 {
		maxTasks = defaultMaxTasks
	}
	if service.taskCount >= maxTasks {
		service.mu.Unlock()

		return &ConfigError{Field: "task", Reason: "would exceed MaxTasks"}
	}
	service.taskCount++
	ctx := service.ctx
	service.mu.Unlock()

	go func() {
		err := invoke(name, "run", task, ctx)

		service.mu.Lock()
		defer service.mu.Unlock()

		if err != nil && !isCancellationResult(ctx, err) {
			componentError := &ComponentError{
				Component: name,
				Operation: "run",
				Err:       err,
			}
			service.taskErrors = append(service.taskErrors, componentError)
			service.cancel(componentError)
			if service.state == StateReady {
				service.state = StateDraining
			}
		}

		service.taskCount--
		if service.taskCount == 0 && service.state == StateStopping && service.stopsDone {
			service.finishShutdownLocked()
		}
	}()

	return nil
}

func isCancellationResult(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() == nil {
		return false
	}

	return errors.Is(err, ctx.Err()) || errors.Is(err, context.Cause(ctx))
}

// Shutdown cancels the service and stops components in reverse startup order.
func (service *Service) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return &ConfigError{Field: "ctx", Reason: "must not be nil"}
	}

	service.mu.Lock()
	if service.state == StateStopped {
		err := service.shutdownErr
		service.mu.Unlock()

		return err
	}
	if service.state == StateNew {
		service.state = StateStopped
		service.mu.Unlock()

		return nil
	}
	if service.state == StateStarting {
		if service.cancel != nil {
			service.cancel(ErrShutdown)
		}
		done := service.startDone
		service.mu.Unlock()

		return service.waitForStartupShutdown(ctx, done)
	}
	if service.state == StateStopping {
		done := service.shutdownDone
		service.mu.Unlock()

		return service.waitForShutdown(ctx, done)
	}
	if service.cancel != nil {
		service.cancel(ErrShutdown)
	}
	service.state = StateStopping
	service.shutdownDone = make(chan struct{})
	service.stopsDone = false
	done := service.shutdownDone
	started := service.started
	service.mu.Unlock()
	go service.stopComponents(ctx, started, "stop", nil)

	return service.waitForShutdown(ctx, done)
}

func (service *Service) stopComponents(
	ctx context.Context,
	started int,
	operation string,
	completed chan<- []error,
) {
	var stopErrors []error
	for index := started - 1; index >= 0; index-- {
		component := service.components[index]
		if component.Stop == nil {
			continue
		}
		if err := invoke(component.Name, operation, component.Stop, ctx); err != nil {
			stopErrors = append(stopErrors,
				&ComponentError{Component: component.Name, Operation: operation, Err: err})
		}
	}

	service.mu.Lock()
	service.started = 0
	service.stopErrors = append(service.stopErrors, stopErrors...)
	service.stopsDone = true
	if service.taskCount == 0 {
		service.finishShutdownLocked()
	}
	service.mu.Unlock()
	if completed != nil {
		completed <- stopErrors
	}
}

func (service *Service) finishShutdownLocked() {
	failures := append(
		append([]error(nil), service.stopErrors...),
		service.taskErrors...,
	)
	if len(failures) > 0 {
		service.shutdownErr = &ShutdownError{Failures: failures}
	} else {
		service.shutdownErr = nil
	}
	service.state = StateStopped
	close(service.shutdownDone)
}

func (service *Service) waitForStartupShutdown(
	ctx context.Context,
	done <-chan struct{},
) error {
	select {
	case <-done:
		service.mu.RLock()
		state := service.state
		shutdownDone := service.shutdownDone
		err := service.shutdownErr
		service.mu.RUnlock()
		if state == StateStopping {
			return service.waitForShutdown(ctx, shutdownDone)
		}

		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (service *Service) waitForShutdown(ctx context.Context, done <-chan struct{}) error {
	select {
	case <-done:
		service.mu.RLock()
		defer service.mu.RUnlock()

		return service.shutdownErr
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (service *Service) failStartup(component string, cause error) error {
	service.cancel(cause)
	rollback := service.beginRollback()
	startupError := &StartupError{
		Component: component,
		Err:       cause,
		Rollback:  rollback,
	}

	service.mu.Lock()
	close(service.startDone)
	service.mu.Unlock()

	return startupError
}

func (service *Service) beginRollback() []error {
	ctx, cancel := context.WithTimeout(context.Background(), service.rollback)
	defer cancel()

	service.mu.Lock()
	started := service.started
	service.state = StateStopping
	service.shutdownDone = make(chan struct{})
	service.stopsDone = false
	service.mu.Unlock()

	completed := make(chan []error, 1)
	go service.stopComponents(ctx, started, "rollback", completed)
	select {
	case rollbackErrors := <-completed:
		return rollbackErrors
	case <-ctx.Done():
		return []error{&ComponentError{
			Component: "service",
			Operation: "rollback",
			Err:       context.Cause(ctx),
		}}
	}
}

func invoke(
	component string,
	operation string,
	function func(context.Context) error,
	ctx context.Context,
) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = &PanicError{
				Component: component,
				Operation: operation,
				Value:     value,
			}
		}
	}()

	return function(ctx)
}
