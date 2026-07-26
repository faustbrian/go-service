// Package servicetest provides deterministic lifecycle and HTTP probe test
// utilities without timing sleeps.
package servicetest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/faustbrian/golib/pkg/service/service"
)

const maximumProbeBody = 16 << 20

// ErrInvalidConfig identifies invalid test utility configuration.
var ErrInvalidConfig = errors.New("invalid service test configuration")

// ConfigError identifies one invalid test utility field.
type ConfigError struct {
	// Field identifies the rejected test configuration path.
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

// Barrier records entry and blocks until release or context cancellation. Its
// zero value is ready for use.
type Barrier struct {
	once        sync.Once
	enterOnce   sync.Once
	releaseOnce sync.Once
	entered     chan struct{}
	release     chan struct{}
}

// Entered closes when the first Wait enters the barrier.
func (barrier *Barrier) Entered() <-chan struct{} {
	barrier.initialize()

	return barrier.entered
}

// Wait records entry and waits for Release or the context cause.
func (barrier *Barrier) Wait(ctx context.Context) error {
	if ctx == nil {
		return &ConfigError{Field: "ctx", Reason: "must not be nil"}
	}
	barrier.initialize()
	barrier.enterOnce.Do(func() { close(barrier.entered) })

	select {
	case <-barrier.release:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Release unblocks current and future Wait calls. Repeated calls are safe.
func (barrier *Barrier) Release() {
	barrier.initialize()
	barrier.releaseOnce.Do(func() { close(barrier.release) })
}

func (barrier *Barrier) initialize() {
	barrier.once.Do(func() {
		barrier.entered = make(chan struct{})
		barrier.release = make(chan struct{})
	})
}

// Recorder stores concurrent event strings. Its zero value is ready for use.
type Recorder struct {
	mu     sync.RWMutex
	events []string
}

// Record appends one event.
func (recorder *Recorder) Record(event string) {
	recorder.mu.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mu.Unlock()
}

// Events returns an immutable snapshot in recording order.
func (recorder *Recorder) Events() []string {
	recorder.mu.RLock()
	defer recorder.mu.RUnlock()

	return append([]string(nil), recorder.events...)
}

// ComponentConfig controls a deterministic lifecycle component.
type ComponentConfig struct {
	// Name is the component's required diagnostic name.
	Name string
	// Recorder optionally records start and stop events.
	Recorder *Recorder
	// StartBarrier optionally blocks Start until released or canceled.
	StartBarrier *Barrier
	// StopBarrier optionally blocks Stop until released or canceled.
	StopBarrier *Barrier
	// StartError is returned after StartBarrier releases.
	StartError error
	// StopError is returned after StopBarrier releases.
	StopError error
}

// NewComponent creates a component whose barriers and failures are entirely
// caller-controlled.
func NewComponent(config ComponentConfig) (service.Component, error) {
	if strings.TrimSpace(config.Name) == "" {
		return service.Component{}, &ConfigError{Field: "Name", Reason: "must not be blank"}
	}

	return service.Component{
		Name: config.Name,
		Start: func(ctx context.Context) error {
			if config.Recorder != nil {
				config.Recorder.Record("start " + config.Name)
			}
			if config.StartBarrier != nil {
				if err := config.StartBarrier.Wait(ctx); err != nil {
					return err
				}
			}

			return config.StartError
		},
		Stop: func(ctx context.Context) error {
			if config.Recorder != nil {
				config.Recorder.Record("stop " + config.Name)
			}
			if config.StopBarrier != nil {
				if err := config.StopBarrier.Wait(ctx); err != nil {
					return err
				}
			}

			return config.StopError
		},
	}, nil
}

// ProbeResult is a bounded in-memory HTTP response.
type ProbeResult struct {
	// Status is the first response status, or 200 when none was written.
	Status int
	// Header is an isolated snapshot of response headers.
	Header http.Header
	// Body contains at most the requested number of response bytes.
	Body []byte
	// Truncated reports whether the handler wrote beyond the capture limit.
	Truncated bool
}

// Probe invokes a handler and captures at most maxBody response bytes.
func Probe(
	handler http.Handler,
	request *http.Request,
	maxBody int,
) (ProbeResult, error) {
	if handler == nil {
		return ProbeResult{}, &ConfigError{Field: "handler", Reason: "must not be nil"}
	}
	if request == nil {
		return ProbeResult{}, &ConfigError{Field: "request", Reason: "must not be nil"}
	}
	if maxBody < 0 || maxBody > maximumProbeBody {
		return ProbeResult{}, &ConfigError{Field: "maxBody", Reason: "is outside the allowed range"}
	}

	writer := newProbeWriter(maxBody)
	handler.ServeHTTP(writer, request)

	return ProbeResult{
		Status:    writer.status,
		Header:    writer.header.Clone(),
		Body:      append([]byte(nil), writer.body...),
		Truncated: writer.truncated,
	}, nil
}

type probeWriter struct {
	header      http.Header
	status      int
	wroteHeader bool
	body        []byte
	limit       int
	truncated   bool
}

func newProbeWriter(limit int) *probeWriter {
	return &probeWriter{
		header: make(http.Header),
		status: http.StatusOK,
		limit:  limit,
		body:   make([]byte, 0, min(limit, 4096)),
	}
}

func (writer *probeWriter) Header() http.Header {
	return writer.header
}

func (writer *probeWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.status = status
}

func (writer *probeWriter) Write(body []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	remaining := writer.limit - len(writer.body)
	if remaining < len(body) {
		writer.truncated = true
	}
	if remaining > 0 {
		retained := min(remaining, len(body))
		writer.body = append(writer.body, body[:retained]...)
	}

	return len(body), nil
}

func (writer *probeWriter) Flush() {}
