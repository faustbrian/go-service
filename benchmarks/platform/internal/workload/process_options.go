package workload

import (
	"context"
	"io"
	"log/slog"
)

type processTraceMarker struct{}

// DisabledOptions returns the compile-time disabled process state.
func DisabledOptions() Options {
	return Options{}
}

// LoggingOptions returns the compile-time logging process state.
func LoggingOptions() Options {
	return Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// TracingOptions returns the compile-time tracing process state.
func TracingOptions() Options {
	return Options{Trace: func(ctx context.Context) context.Context {
		return context.WithValue(ctx, processTraceMarker{}, true)
	}}
}
