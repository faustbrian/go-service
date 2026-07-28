package main

import (
	"context"
	"net/http"
	"os"
	"strings"

	"github.com/faustbrian/golib/pkg/service"
)

type configuration struct {
	businessAddress   string
	managementAddress string
}

func main() {
	os.Exit(service.Main(service.Definition{
		Identity: service.Identity{Name: "ingester"},
		Commands: service.Commands{Custom: []service.Command{
			service.CommandFor(service.CommandSpec[configuration]{
				Name:    "ingest",
				Summary: "accept ingestion requests",
				Kind:    service.CommandKindLongRunning,
				Load:    load,
				Build:   build,
			}),
		}},
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
	handler := http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.WriteHeader(http.StatusAccepted)
	})

	return service.Plan{
		HTTP: &service.HTTP{
			Address: configuration.businessAddress,
			Handler: handler,
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
