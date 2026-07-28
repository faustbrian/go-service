package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/faustbrian/golib/pkg/service"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

type configuration struct {
	businessAddress   string
	managementAddress string
}

func main() {
	os.Exit(service.Main(service.Definition{
		Identity: service.Identity{Name: "http-api"},
		Commands: service.Commands{Serve: service.CommandFor(
			service.CommandSpec[configuration]{
				Name:    "serve",
				Summary: "serve the HTTP API",
				Kind:    service.CommandKindLongRunning,
				Load:    load,
				Build:   build,
			},
		)},
	}))
}

func load(
	_ context.Context,
	invocation service.Invocation,
) (configuration, error) {
	return configuration{
		businessAddress: environment(
			invocation.Environment,
			"LISTEN_ADDRESS",
			"127.0.0.1:8080",
		),
		managementAddress: environment(
			invocation.Environment,
			"MANAGEMENT_ADDRESS",
			"",
		),
	}, nil
}

func build(
	_ context.Context,
	_ service.BuildContext,
	configuration configuration,
) (service.Plan, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("go-service\n"))
	})

	return service.Plan{
		HTTP: &service.HTTP{
			Address: configuration.businessAddress,
			Handler: mux,
			Options: []serverhttp.Option{
				serverhttp.WithShutdownTimeout(20 * time.Second),
			},
		},
		ManagementConfig: &service.Management{
			Address: configuration.managementAddress,
		},
	}, nil
}

func environment(values []string, name string, fallback string) string {
	prefix := name + "="
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}

	return fallback
}
