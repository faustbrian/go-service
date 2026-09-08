package integration_test

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"time"

	"github.com/faustbrian/go-service"
	"github.com/faustbrian/go-service/healthhttp"
	"github.com/faustbrian/go-service/integration"
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

// ExampleNew_serviceWithHealth demonstrates the smallest HTTP service
// composition: caller-owned startup is attached to the service lifecycle and
// the health endpoint is exposed only after the runtime has started.
func ExampleNew_serviceWithHealth() {
	component, err := integration.New("database", integration.Hooks{
		Start: func(context.Context) error {
			fmt.Println("database ready")

			return nil
		},
	})
	if err != nil {
		panic(err)
	}
	runtime, err := service.New(service.Config{Components: []service.Component{component}})
	if err != nil {
		panic(err)
	}
	probes, err := healthhttp.New(healthhttp.Config{Checks: []healthhttp.Check{{
		Name: "database", Run: func(context.Context) error { return nil },
	}}})
	if err != nil {
		panic(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		panic(err)
	}
	defer func() { _ = runtime.Shutdown(context.Background()) }()
	recorder := httptest.NewRecorder()
	probes.Readiness().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/ready", nil))
	fmt.Println(recorder.Code)
	// Output:
	// database ready
	// 200
}
