package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"
)

const defaultShutdownTimeout = 30 * time.Second

// ErrSignal identifies cancellation initiated by a process signal.
var ErrSignal = errors.New("service signal")

// SignalError records the signal that initiated shutdown.
type SignalError struct {
	// Signal is the process signal that initiated cancellation.
	Signal os.Signal
}

// Error implements error.
func (err *SignalError) Error() string {
	return ErrSignal.Error()
}

// Unwrap makes SignalError inspectable with errors.Is.
func (err *SignalError) Unwrap() error {
	return ErrSignal
}

// RunConfig controls process signal handling and the shutdown bound.
type RunConfig struct {
	// Signals is the set that initiates shutdown. Empty uses platform defaults.
	Signals []os.Signal
	// ShutdownTimeout bounds waiting for cleanup after cancellation. Zero uses the
	// 30-second default. This bounds waiting, not arbitrary callback execution.
	ShutdownTimeout time.Duration
}

// Run starts a service, owns an OS signal subscription until shutdown, and
// stops the service after parent cancellation or the first configured signal.
// Defaults are interrupt and SIGTERM on Unix, interrupt elsewhere. A second
// signal cancels the cleanup context but retains the original deadline.
// Successful cleanup returns nil; a signal is retained in the service Context
// cause, while cleanup failures are returned. Startup failure is returned
// directly. It does not call os.Exit.
func Run(ctx context.Context, runtime *Service, config RunConfig) error {
	if err := validateRun(ctx, runtime, config.ShutdownTimeout); err != nil {
		return err
	}
	if err := validateSignals(config.Signals); err != nil {
		return err
	}

	signals := configuredSignals(config.Signals)

	signalChannel := make(chan os.Signal, 2)
	signal.Notify(signalChannel, signals...)
	defer signal.Stop(signalChannel)

	return RunWithSignals(
		ctx,
		runtime,
		config.ShutdownTimeout,
		signalChannel,
	)
}

func configuredSignals(configured []os.Signal) []os.Signal {
	if len(configured) == 0 {
		return defaultSignals()
	}

	return append([]os.Signal(nil), configured...)
}

// RunWithSignals starts and stops a service using a caller-owned signal
// channel. It rejects nil contexts, service or channel and negative timeout;
// zero timeout selects 30 seconds. It never closes or unregisters the channel.
// Closing the channel initiates cancellation with ErrSignal. Its lifecycle and
// error behavior otherwise follows Run; caller code must keep signal senders
// independent of cleanup callbacks.
func RunWithSignals(
	ctx context.Context,
	runtime *Service,
	shutdownTimeout time.Duration,
	signals <-chan os.Signal,
) error {
	if err := validateRun(ctx, runtime, shutdownTimeout); err != nil {
		return err
	}
	if signals == nil {
		return &ConfigError{Field: "signals", Reason: "must not be nil"}
	}
	if shutdownTimeout == 0 {
		shutdownTimeout = defaultShutdownTimeout
	}

	if err := runtime.Start(ctx); err != nil {
		return err
	}

	return waitWithSignals(ctx, runtime, shutdownTimeout, signals)
}

// Wait waits for cancellation or an owned OS signal and then stops an already
// started service. It is intended for runtimes that register supervised work
// between Start and Wait.
func Wait(ctx context.Context, runtime *Service, config RunConfig) error {
	if err := validateRun(ctx, runtime, config.ShutdownTimeout); err != nil {
		return err
	}
	if err := validateSignals(config.Signals); err != nil {
		return err
	}

	signals := configuredSignals(config.Signals)

	signalChannel := make(chan os.Signal, 2)
	signal.Notify(signalChannel, signals...)
	defer signal.Stop(signalChannel)

	return WaitWithSignals(
		ctx,
		runtime,
		config.ShutdownTimeout,
		signalChannel,
	)
}

// WaitWithSignals waits using a caller-owned signal channel and stops an
// already started ready or draining service. Nil channel is rejected; closure
// initiates ErrSignal cancellation. It never closes or unregisters the channel.
// Shutdown failures are returned; initiating cancellation alone is not an error.
func WaitWithSignals(
	ctx context.Context,
	runtime *Service,
	shutdownTimeout time.Duration,
	signals <-chan os.Signal,
) error {
	if err := validateRun(ctx, runtime, shutdownTimeout); err != nil {
		return err
	}
	if signals == nil {
		return &ConfigError{Field: "signals", Reason: "must not be nil"}
	}
	state := runtime.State()
	if state != StateReady && state != StateDraining {
		return &StateError{Operation: "wait", State: state}
	}
	if shutdownTimeout == 0 {
		shutdownTimeout = defaultShutdownTimeout
	}

	return waitWithSignals(ctx, runtime, shutdownTimeout, signals)
}

func waitWithSignals(
	ctx context.Context,
	runtime *Service,
	shutdownTimeout time.Duration,
	signals <-chan os.Signal,
) error {
	select {
	case received, open := <-signals:
		if open {
			runtime.cancelWithCause(&SignalError{Signal: received})
		} else {
			runtime.cancelWithCause(ErrSignal)
		}
	case <-ctx.Done():
		runtime.cancelWithCause(context.Cause(ctx))
	case <-runtime.Context().Done():
	}

	return shutdownWithEscalation(runtime, shutdownTimeout, signals)
}

func shutdownWithEscalation(
	runtime *Service,
	shutdownTimeout time.Duration,
	signals <-chan os.Signal,
) error {
	shutdownContext, cancel := newShutdownContext(runtime, shutdownTimeout)
	defer cancel()

	shutdownResult := make(chan error, 1)
	go func() {
		shutdownResult <- budgetedShutdown(runtime, shutdownContext)
	}()

	select {
	case err := <-shutdownResult:
		return classifyShutdownError(err)
	case _, open := <-signals:
		if !open {
			return classifyShutdownError(<-shutdownResult)
		}
		// Escalation stops cooperative cleanup but does not grant a fresh
		// timeout or prove that callbacks have terminated.
		deadline, _ := shutdownDeadline(shutdownContext)
		cancel()
		<-shutdownResult
		completionContext, completionCancel := context.WithDeadline(
			context.Background(),
			deadline,
		)
		defer completionCancel()

		return classifyShutdownError(runtime.Shutdown(completionContext))
	}
}

func validateRun(
	ctx context.Context,
	runtime *Service,
	shutdownTimeout time.Duration,
) error {
	if ctx == nil {
		return &ConfigError{Field: "ctx", Reason: "must not be nil"}
	}
	if runtime == nil {
		return &ConfigError{Field: "service", Reason: "must not be nil"}
	}
	if shutdownTimeout < 0 {
		return &ConfigError{
			Field:  "ShutdownTimeout",
			Reason: "must not be negative",
		}
	}

	return nil
}

func validateSignals(signals []os.Signal) error {
	for index, configured := range signals {
		if configured == nil {
			return &ConfigError{
				Field:  fmt.Sprintf("Signals[%d]", index),
				Reason: "must not be nil",
			}
		}
	}

	return nil
}

func (service *Service) cancelWithCause(cause error) {
	service.mu.RLock()
	state := service.state
	service.mu.RUnlock()
	if state == StateReady || state == StateDraining {
		_ = service.Drain()
	}

	service.mu.Lock()
	defer service.mu.Unlock()

	if service.cancel != nil {
		service.cancel(cause)
	}
}
