package service_test

import (
	"context"
	"fmt"
	"io"

	"github.com/faustbrian/go-cli"

	"github.com/faustbrian/go-service/v2"
)

func ExampleService() {
	runtime, err := service.New(service.Config{
		Components: []service.Component{{
			Name:  "worker",
			Start: func(context.Context) error { return nil },
			Stop:  func(context.Context) error { return nil },
		}},
	})
	if err != nil {
		panic(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		panic(err)
	}
	fmt.Println(runtime.State())
	if err := runtime.Shutdown(context.Background()); err != nil {
		panic(err)
	}
	fmt.Println(runtime.State())
	// Output:
	// ready
	// stopped
}

func ExampleInvocation_Input() {
	address := cli.StringOption("address").Default("127.0.0.1:8080")
	command := service.CommandFor(service.CommandSpec[string]{
		Name: "migrate", Kind: service.CommandKindOneShot,
		Options: []cli.OptionDefinition{address},
		Load: func(_ context.Context, invocation service.Invocation) (string, error) {
			return address.Get(invocation.Input()), nil
		},
		Build: func(_ context.Context, _ service.BuildContext, address string) (service.Plan, error) {
			fmt.Println(address)
			return service.Plan{}, nil
		},
	})
	exit := service.Execute(context.Background(), service.Definition{
		Identity: service.Identity{Name: "example"},
		Commands: service.Commands{Migrate: command},
	}, service.Invocation{
		Args: []string{"migrate", "--address=:9000"}, Stdout: io.Discard, Stderr: io.Discard,
	})
	if exit != 0 {
		fmt.Println("command failed")
	}
	// Output: :9000
}
