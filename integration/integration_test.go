package integration_test

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"testing"

	"github.com/faustbrian/golib/pkg/service/integration"
	"github.com/faustbrian/golib/pkg/service/service"
)

func TestHookComponentPreventsPartialStartupAndKeepsCleanupOrdered(t *testing.T) {
	t.Parallel()

	loadFailure := errors.New("secret configuration failure")
	var events []string
	configuration, err := integration.New("configuration", integration.Hooks{
		Start: func(context.Context) error {
			events = append(events, "load configuration")

			return loadFailure
		},
	})
	if err != nil {
		t.Fatalf("integration.New() error = %v", err)
	}
	runtime, err := service.New(service.Config{Components: []service.Component{
		configuration,
		{
			Name: "worker",
			Start: func(context.Context) error {
				events = append(events, "start worker")

				return nil
			},
		},
	}})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); !errors.Is(err, loadFailure) {
		t.Fatalf("Start() error = %v, want load failure", err)
	}
	if !slices.Equal(events, []string{"load configuration"}) {
		t.Fatalf("events = %v", events)
	}
}

func TestSlogOptionReportsStatusWithoutErrorValues(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{}
	logger := slog.New(handler)
	hookFailure := errors.New("secret hook failure")
	component, err := integration.New(
		"telemetry",
		integration.Hooks{Start: func(context.Context) error { return hookFailure }},
		integration.WithSlog(logger, slog.String("role", "ingester")),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := component.Start(context.Background()); !errors.Is(err, hookFailure) {
		t.Fatalf("Start() error = %v, want hook failure", err)
	}

	messages := handler.Messages()
	want := []string{"integration starting", "integration start failed"}
	if !slices.Equal(messages, want) {
		t.Fatalf("messages = %v, want %v", messages, want)
	}
	for _, message := range messages {
		if message == hookFailure.Error() {
			t.Fatalf("log message leaked hook failure: %q", message)
		}
	}
	attributes := handler.Attributes()
	wantAttributes := map[string]string{
		"component": "telemetry",
		"role":      "ingester",
	}
	if !reflect.DeepEqual(attributes, wantAttributes) {
		t.Fatalf("attributes = %v, want %v", attributes, wantAttributes)
	}
}

func TestHooksReceiveCallerContext(t *testing.T) {
	t.Parallel()

	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "trace-context")
	values := make(chan any, 2)
	component, err := integration.New("telemetry", integration.Hooks{
		Start: func(ctx context.Context) error {
			values <- ctx.Value(contextKey{})

			return nil
		},
		Stop: func(ctx context.Context) error {
			values <- ctx.Value(contextKey{})

			return nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := component.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := component.Stop(ctx); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	for range 2 {
		if value := <-values; value != "trace-context" {
			t.Fatalf("hook context value = %v", value)
		}
	}
}

func TestHookComponentRunsSuccessAndStopFailurePaths(t *testing.T) {
	t.Parallel()

	handler := &recordingHandler{}
	logger := slog.New(handler)
	var events []string
	component, err := integration.New(
		"queue",
		integration.Hooks{
			Start: func(context.Context) error {
				events = append(events, "start")

				return nil
			},
			Stop: func(context.Context) error {
				events = append(events, "stop")

				return nil
			},
		},
		integration.WithSlog(logger),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := component.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := component.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !slices.Equal(events, []string{"start", "stop"}) {
		t.Fatalf("events = %v", events)
	}

	optional, err := integration.New(
		"optional",
		integration.Hooks{},
		integration.WithSlog(logger),
	)
	if err != nil {
		t.Fatalf("New() optional error = %v", err)
	}
	if err := optional.Start(context.Background()); err != nil {
		t.Fatalf("optional Start() error = %v", err)
	}
	if err := optional.Stop(context.Background()); err != nil {
		t.Fatalf("optional Stop() error = %v", err)
	}

	stopFailure := errors.New("stop failure")
	failing, err := integration.New(
		"failing",
		integration.Hooks{Stop: func(context.Context) error { return stopFailure }},
		integration.WithSlog(logger),
	)
	if err != nil {
		t.Fatalf("New() failing error = %v", err)
	}
	if err := failing.Stop(context.Background()); !errors.Is(err, stopFailure) {
		t.Fatalf("Stop() error = %v, want stop failure", err)
	}
}

func TestIntegrationConfigurationValidation(t *testing.T) {
	t.Parallel()

	attributes := make([]slog.Attr, 33)
	for index := range attributes {
		attributes[index] = slog.Int("key", index)
	}
	tests := map[string]func() error{
		"blank name": func() error {
			_, err := integration.New(" ", integration.Hooks{})

			return err
		},
		"nil option": func() error {
			_, err := integration.New("hook", integration.Hooks{}, nil)

			return err
		},
		"nil logger": func() error {
			_, err := integration.New(
				"hook",
				integration.Hooks{},
				integration.WithSlog(nil),
			)

			return err
		},
		"too many attributes": func() error {
			_, err := integration.New(
				"hook",
				integration.Hooks{},
				integration.WithSlog(slog.Default(), attributes...),
			)

			return err
		},
		"blank attribute": func() error {
			_, err := integration.New(
				"hook",
				integration.Hooks{},
				integration.WithSlog(slog.Default(), slog.String("", "value")),
			)

			return err
		},
		"duplicate logger": func() error {
			_, err := integration.New(
				"hook",
				integration.Hooks{},
				integration.WithSlog(slog.Default()),
				integration.WithSlog(slog.Default()),
			)

			return err
		},
	}
	for name, operation := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := operation()
			if !errors.Is(err, integration.ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
			var configError *integration.ConfigError
			if !errors.As(err, &configError) || configError.Error() == "" {
				t.Fatalf("error = %#v, want ConfigError", err)
			}
		})
	}
}

type recordingHandler struct {
	records []slog.Record
}

func (handler *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	handler.records = append(handler.records, record.Clone())

	return nil
}

func (handler *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *recordingHandler) WithGroup(string) slog.Handler      { return handler }

func (handler *recordingHandler) Messages() []string {
	messages := make([]string, 0, len(handler.records))
	for _, record := range handler.records {
		messages = append(messages, record.Message)
	}

	return messages
}

func (handler *recordingHandler) Attributes() map[string]string {
	attributes := make(map[string]string)
	for _, record := range handler.records {
		record.Attrs(func(attribute slog.Attr) bool {
			attributes[attribute.Key] = attribute.Value.String()

			return true
		})
	}

	return attributes
}
