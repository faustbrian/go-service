package service

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/faustbrian/golib/pkg/correlation"
	correlationlog "github.com/faustbrian/golib/pkg/correlation/log"
)

// RuntimeEventKind identifies one bounded platform-managed runtime boundary.
type RuntimeEventKind string

const (
	// RuntimeEventStartup reports process startup boundaries.
	RuntimeEventStartup RuntimeEventKind = "startup"
	// RuntimeEventConstruction reports selected-role configuration and plan construction.
	RuntimeEventConstruction RuntimeEventKind = "construction"
	// RuntimeEventReadiness reports readiness availability transitions.
	RuntimeEventReadiness RuntimeEventKind = "readiness"
	// RuntimeEventDrain reports the beginning of graceful drain.
	RuntimeEventDrain RuntimeEventKind = "drain"
	// RuntimeEventShutdown reports bounded shutdown completion.
	RuntimeEventShutdown RuntimeEventKind = "shutdown"
	// RuntimeEventTask reports platform-supervised task execution.
	RuntimeEventTask RuntimeEventKind = "task"
	// RuntimeEventComponentStart reports owned component initialization.
	RuntimeEventComponentStart RuntimeEventKind = "component-start"
	// RuntimeEventComponentStop reports owned component cleanup.
	RuntimeEventComponentStop RuntimeEventKind = "component-stop"
	// RuntimeEventProbe reports one management probe result.
	RuntimeEventProbe RuntimeEventKind = "probe"
	// RuntimeEventMaintenance reports maintenance-state changes or refresh failures.
	RuntimeEventMaintenance RuntimeEventKind = "maintenance"
	// RuntimeEventRequest reports one platform-managed business HTTP request.
	RuntimeEventRequest RuntimeEventKind = "request"
)

// RuntimeEventResult is the bounded result vocabulary used by runtime events.
type RuntimeEventResult string

const (
	// RuntimeResultStarted reports the beginning of owned work.
	RuntimeResultStarted RuntimeEventResult = "started"
	// RuntimeResultSucceeded reports successful completion.
	RuntimeResultSucceeded RuntimeEventResult = "succeeded"
	// RuntimeResultFailed reports failure without exposing its cause.
	RuntimeResultFailed RuntimeEventResult = "failed"
	// RuntimeResultAvailable reports an available readiness or probe state.
	RuntimeResultAvailable RuntimeEventResult = "available"
	// RuntimeResultUnavailable reports an unavailable readiness or probe state.
	RuntimeResultUnavailable RuntimeEventResult = "unavailable"
)

// RuntimeEvent is a bounded, secret-safe platform observation. Boundary is a
// validated component, task, probe, or maintenance-store name. Identity values
// are suitable for logs and telemetry resources; Environment and Instance must
// not be used as metric labels.
type RuntimeEvent struct {
	// Kind identifies the platform boundary.
	Kind RuntimeEventKind
	// Result is the bounded outcome classification.
	Result RuntimeEventResult
	// Identity identifies the service process and selected role.
	Identity ProcessIdentity
	// Boundary names the validated component, task, probe, or store boundary.
	Boundary string
	// Duration is the measured operation duration when applicable.
	Duration time.Duration
	// Transition reports an availability or lifecycle state transition.
	Transition bool
	// Method is a bounded HTTP method for request events only.
	Method string
	// Status is an HTTP response status for request events only.
	Status int
	// At is the event publication time.
	At time.Time
}

// RuntimeObserver receives synchronous low-volume platform observations. An
// implementation must handle concurrent calls, return promptly, and must not
// panic. The platform contains panics and never transfers ownership of the
// observer.
type RuntimeObserver interface {
	// ObserveRuntime receives one completed bounded platform observation.
	ObserveRuntime(context.Context, RuntimeEvent)
}

// RuntimeObserverFunc adapts a function to RuntimeObserver.
type RuntimeObserverFunc func(context.Context, RuntimeEvent)

// ObserveRuntime invokes the adapted observer.
func (observe RuntimeObserverFunc) ObserveRuntime(ctx context.Context, event RuntimeEvent) {
	observe(ctx, event)
}

type runtimeObservability struct {
	identity       ProcessIdentity
	identityLogger *slog.Logger
	logger         *slog.Logger
	observer       RuntimeObserver
	disclosure     correlation.DisclosurePolicy
	ready          atomic.Bool
	draining       atomic.Bool
	probeMu        sync.Mutex
	probes         map[string]bool
}

func newRuntimeObservability(
	ctx context.Context,
	identity ProcessIdentity,
	logger *slog.Logger,
	observer RuntimeObserver,
	disclosure correlation.DisclosurePolicy,
) *runtimeObservability {
	if logger == nil && observer == nil {
		return nil
	}
	identityLogger := logger
	if logger != nil {
		identityLogger = logger.With(identityLogAttributes(identity)...)
		logger = identityLogger
		if values, ok := correlation.FromContext(ctx); ok {
			if attributes, err := correlationlog.Attrs(values, disclosure); err == nil {
				logger = logger.With(slogAttributes(attributes)...)
			}
		}
	}

	return &runtimeObservability{
		identity: identity, identityLogger: identityLogger, logger: logger,
		observer: observer, disclosure: disclosure,
		probes: make(map[string]bool, 3),
	}
}

func identityLogAttributes(identity ProcessIdentity) []any {
	attributes := make([]any, 0, 16)
	appendValue := func(key, value string) {
		if value != "" {
			attributes = append(attributes, slog.String(key, value))
		}
	}
	appendValue("service.name", identity.Name)
	appendValue("service.version", identity.Version)
	appendValue("service.commit", identity.Commit)
	appendValue("service.build_time", identity.BuildTime)
	appendValue("service.go_version", identity.GoVersion)
	appendValue("deployment.environment", identity.Environment)
	appendValue("service.instance.id", identity.Instance)
	appendValue("process.role", identity.Role)

	return attributes
}

func slogAttributes(attributes []slog.Attr) []any {
	values := make([]any, len(attributes))
	for index := range attributes {
		values[index] = attributes[index]
	}

	return values
}

func (observability *runtimeObservability) event(
	ctx context.Context,
	kind RuntimeEventKind,
	result RuntimeEventResult,
	boundary string,
	duration time.Duration,
	transition bool,
) {
	observability.publish(ctx, kind, result, boundary, duration, transition, true)
}

func (observability *runtimeObservability) publish(
	ctx context.Context,
	kind RuntimeEventKind,
	result RuntimeEventResult,
	boundary string,
	duration time.Duration,
	transition bool,
	writeLog bool,
) {
	if observability == nil {
		return
	}
	event := RuntimeEvent{
		Kind: kind, Result: result, Identity: observability.identity,
		Boundary: boundary, Duration: duration, Transition: transition, At: time.Now(),
	}
	logger := observability.loggerForContext(ctx)
	if writeLog && logger != nil {
		attributes := []slog.Attr{
			slog.String("event.kind", string(kind)),
			slog.String("event.result", string(result)),
		}
		if boundary != "" {
			attributes = append(attributes, slog.String("event.boundary", boundary))
		}
		if duration > 0 {
			attributes = append(attributes, slog.Duration("event.duration", duration))
		}
		if transition {
			attributes = append(attributes, slog.Bool("event.transition", true))
		}
		level := slog.LevelInfo
		if result == RuntimeResultFailed || result == RuntimeResultUnavailable {
			level = slog.LevelError
		}
		logger.LogAttrs(ctx, level, "service runtime event", attributes...)
	}
	if observability.observer != nil {
		observability.observe(ctx, event)
	}
}

func (observability *runtimeObservability) loggerForContext(ctx context.Context) *slog.Logger {
	if observability == nil || observability.identityLogger == nil {
		return nil
	}
	values, ok := correlation.FromContext(ctx)
	if !ok {
		return observability.identityLogger
	}
	attributes, err := correlationlog.Attrs(values, observability.disclosure)
	if err != nil {
		return observability.identityLogger
	}

	return observability.identityLogger.With(slogAttributes(attributes)...)
}

func (observability *runtimeObservability) request(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		tracked := &observedResponseWriter{ResponseWriter: writer, status: http.StatusOK}
		started := time.Now()
		defer func() {
			if value := recover(); value != nil {
				if !tracked.wroteHeader {
					tracked.status = http.StatusInternalServerError
				}
				observability.finishRequest(request, tracked.status, started, true)
				panic(value)
			}
		}()
		next.ServeHTTP(tracked, request)
		observability.finishRequest(request, tracked.status, started, false)
	})
}

func (observability *runtimeObservability) finishRequest(
	request *http.Request,
	status int,
	started time.Time,
	panicked bool,
) {
	result := RuntimeResultSucceeded
	if panicked || status >= http.StatusInternalServerError {
		result = RuntimeResultFailed
	}
	event := RuntimeEvent{
		Kind: RuntimeEventRequest, Result: result, Identity: observability.identity,
		Duration: time.Since(started), Method: boundedHTTPMethod(request.Method),
		Status: status, At: time.Now(),
	}
	if logger := observability.loggerForContext(request.Context()); logger != nil {
		level := slog.LevelInfo
		if result == RuntimeResultFailed {
			level = slog.LevelError
		}
		logger.LogAttrs(
			request.Context(), level, "service HTTP request",
			slog.String("event.kind", string(event.Kind)),
			slog.String("event.result", string(event.Result)),
			slog.String("http.request.method", event.Method),
			slog.Int("http.response.status_code", event.Status),
			slog.Duration("event.duration", event.Duration),
		)
	}
	if observability.observer != nil {
		observability.observe(request.Context(), event)
	}
}

func observeComponents(
	components []Component,
	observability *runtimeObservability,
) []Component {
	if observability == nil {
		return components
	}
	observed := make([]Component, len(components))
	for index, component := range components {
		current := component
		if current.Start != nil {
			current.Start = observeComponentOperation(
				current.Name, RuntimeEventComponentStart, current.Start, observability,
			)
		}
		if current.Stop != nil {
			current.Stop = observeComponentOperation(
				current.Name, RuntimeEventComponentStop, current.Stop, observability,
			)
		}
		observed[index] = current
	}

	return observed
}

func observeComponentOperation(
	name string,
	kind RuntimeEventKind,
	operation func(context.Context) error,
	observability *runtimeObservability,
) func(context.Context) error {
	return func(ctx context.Context) (err error) {
		observability.event(ctx, kind, RuntimeResultStarted, name, 0, false)
		started := time.Now()
		defer func() {
			if value := recover(); value != nil {
				observability.event(
					ctx, kind, RuntimeResultFailed, name, time.Since(started), false,
				)
				panic(value)
			}
			result := RuntimeResultSucceeded
			if err != nil {
				result = RuntimeResultFailed
			}
			observability.event(ctx, kind, result, name, time.Since(started), false)
		}()

		return operation(ctx)
	}
}

type observedResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (writer *observedResponseWriter) WriteHeader(status int) {
	if writer.wroteHeader {
		return
	}
	writer.wroteHeader = true
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *observedResponseWriter) Write(body []byte) (int, error) {
	if !writer.wroteHeader {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *observedResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func boundedHTTPMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodOptions,
		http.MethodTrace:
		return method
	default:
		return "OTHER"
	}
}

func (observability *runtimeObservability) probe(
	ctx context.Context,
	probe string,
	available bool,
	duration time.Duration,
) {
	if observability == nil {
		return
	}
	observability.probeMu.Lock()
	previous, known := observability.probes[probe]
	transition := !known || previous != available
	observability.probes[probe] = available
	observability.probeMu.Unlock()
	result := RuntimeResultUnavailable
	if available {
		result = RuntimeResultAvailable
	}
	observability.publish(
		ctx, RuntimeEventProbe, result, probe, duration, transition, transition,
	)
}

func (observability *runtimeObservability) observe(ctx context.Context, event RuntimeEvent) {
	defer func() {
		if value := recover(); value != nil && observability.logger != nil {
			observability.logger.LogAttrs(
				ctx,
				slog.LevelError,
				"service runtime observer failed",
				slog.String("event.kind", string(event.Kind)),
				slog.String("error.kind", "observer-panic"),
			)
		}
	}()
	observability.observer.ObserveRuntime(ctx, event)
}

func (observability *runtimeObservability) markReady(ctx context.Context) {
	if observability == nil || observability.ready.Swap(true) {
		return
	}
	observability.event(ctx, RuntimeEventReadiness, RuntimeResultAvailable, "", 0, true)
}

func (observability *runtimeObservability) beginDrain(ctx context.Context) {
	if observability == nil || observability.draining.Swap(true) {
		return
	}
	if observability.ready.Swap(false) {
		observability.event(ctx, RuntimeEventReadiness, RuntimeResultUnavailable, "", 0, true)
	}
	observability.event(ctx, RuntimeEventDrain, RuntimeResultStarted, "", 0, true)
}

func (observability *runtimeObservability) finishShutdown(
	ctx context.Context,
	started time.Time,
	err error,
) {
	result := RuntimeResultSucceeded
	if err != nil {
		result = RuntimeResultFailed
	}
	observability.event(ctx, RuntimeEventShutdown, result, "", time.Since(started), true)
}

func firstObservability(values []*runtimeObservability) *runtimeObservability {
	if len(values) == 0 {
		return nil
	}

	return values[0]
}
