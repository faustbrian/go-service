package main

import (
	"context"
	"net/http"
	"net/rpc"
	"os"
	"strings"

	"github.com/faustbrian/golib/pkg/service"
)

type statusService struct{}

func (statusService) Ping(_ string, reply *string) error {
	*reply = "pong"

	return nil
}

type configuration struct {
	businessAddress   string
	managementAddress string
}

func main() {
	os.Exit(service.Main(service.Definition{
		Identity: service.Identity{Name: "rpc-service"},
		Commands: service.Commands{Serve: service.CommandFor(
			service.CommandSpec[configuration]{
				Name:    "serve",
				Summary: "serve RPC requests",
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
	rpcServer := rpc.NewServer()
	if err := rpcServer.RegisterName("Status", statusService{}); err != nil {
		return service.Plan{}, err
	}
	mux := http.NewServeMux()
	mux.Handle(rpc.DefaultRPCPath, rpcServer)

	return service.Plan{
		HTTP: &service.HTTP{
			Address: configuration.businessAddress,
			Handler: mux,
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
