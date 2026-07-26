package integration_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/faustbrian/golib/pkg/service/integration"
)

func FuzzOptions(fuzz *testing.F) {
	fuzz.Add("configuration", "role", "worker", true)
	fuzz.Add("", "", "", false)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	fuzz.Fuzz(func(t *testing.T, name, key, value string, logging bool) {
		var options []integration.Option
		if logging {
			options = append(options, integration.WithSlog(
				logger,
				slog.String(key, value),
			))
		}
		component, err := integration.New(name, integration.Hooks{}, options...)
		if err != nil {
			return
		}
		if err := component.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if err := component.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
	})
}
