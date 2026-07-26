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
	return fmt.Sprintf("%v: %v", ErrSignal, err.Signal)
}

// Unwrap makes SignalError inspectable with errors.Is.
func (err *SignalError) Unwrap() error {
	return ErrSignal
}

// RunConfig controls process signal handling and the shutdown bound.
type RunConfig struct {
	// Signals is the set that initiates shutdown. Empty uses platform defaults.
	Signals []os.Signal
	// ShutdownTimeout bounds cleanup after cancellation. Zero uses the
	// documented default.
	ShutdownTimeout time.Duration
}

// Run starts a service, owns an OS signal subscription until shutdown, and
// stops the service after parent cancellation or the first configured signal.
func Run(ctx context.Context, runtime *Service, config RunConfig) error {
	if err := validateRun(ctx, runtime, config.ShutdownTimeout); err != nil {
		return err
	}
	if err := validateSignals(config.Signals); err != nil {
		return err
	}

	signals := append([]os.Signal(nil), config.Signals...)
	if len(signals) == 0 {
		signals = defaultSignals()
	}

	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, signals...)
	defer signal.Stop(signalChannel)

	return RunWithSignals(
		ctx,
		runtime,
		config.ShutdownTimeout,
		signalChannel,
	)
}

// RunWithSignals starts and stops a service using a caller-owned signal
// channel. It never closes or unregisters the channel.
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

	signals := append([]os.Signal(nil), config.Signals...)
	if len(signals) == 0 {
		signals = defaultSignals()
	}

	signalChannel := make(chan os.Signal, 1)
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
// already started service. It never closes or unregisters the channel.
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

	shutdownContext, cancel := context.WithTimeout(
		context.Background(),
		shutdownTimeout,
	)
	defer cancel()

	return runtime.Shutdown(shutdownContext)
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
	service.mu.Lock()
	defer service.mu.Unlock()

	if service.cancel != nil {
		service.cancel(cause)
	}
	if service.state == StateReady {
		service.state = StateDraining
	}
}
