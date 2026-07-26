package integration_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/faustbrian/golib/pkg/service/integration"
	"github.com/faustbrian/golib/pkg/service/service"
)

func ExampleNew() {
	component, err := integration.New("configuration", integration.Hooks{
		Start: func(context.Context) error {
			fmt.Println("load and validate configuration")

			return nil
		},
	})
	if err != nil {
		panic(err)
	}
	if err := component.Start(context.Background()); err != nil {
		panic(err)
	}
	// Output:
	// load and validate configuration
}

func ExampleWithSlog() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
			if attribute.Key == slog.TimeKey {
				return slog.Attr{}
			}

			return attribute
		},
	}))
	component, err := integration.New(
		"telemetry",
		integration.Hooks{
			Start: func(context.Context) error {
				fmt.Println("register caller-owned provider")

				return nil
			},
		},
		integration.WithSlog(logger, slog.Duration("timeout", time.Second)),
	)
	if err != nil {
		panic(err)
	}
	if err := component.Start(context.Background()); err != nil {
		panic(err)
	}
	// Output:
	// level=INFO msg="integration starting" component=telemetry timeout=1s
	// register caller-owned provider
	// level=INFO msg="integration started" component=telemetry timeout=1s
}

func ExampleNew_queueAndScheduler() {
	queue, err := integration.New("queue", integration.Hooks{
		Start: func(context.Context) error {
			fmt.Println("start queue")

			return nil
		},
		Stop: func(context.Context) error {
			fmt.Println("release queue")

			return nil
		},
	})
	if err != nil {
		panic(err)
	}
	scheduler, err := integration.New("scheduler", integration.Hooks{
		Start: func(context.Context) error {
			fmt.Println("start scheduler")

			return nil
		},
		Stop: func(context.Context) error {
			fmt.Println("drain scheduler")

			return nil
		},
	})
	if err != nil {
		panic(err)
	}
	runtime, err := service.New(service.Config{
		Components: []service.Component{queue, scheduler},
	})
	if err != nil {
		panic(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		panic(err)
	}
	if err := runtime.Go("scheduler", func(ctx context.Context) error {
		<-ctx.Done()

		return nil
	}); err != nil {
		panic(err)
	}
	shutdownContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runtime.Shutdown(shutdownContext); err != nil {
		panic(err)
	}
	// Output:
	// start queue
	// start scheduler
	// drain scheduler
	// release queue
}
