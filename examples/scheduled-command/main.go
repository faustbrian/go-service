package main

import (
	"context"
	"log"

	"github.com/faustbrian/golib/pkg/service/service"
)

func main() {
	command := service.Component{
		Name: "command",
		Start: func(ctx context.Context) error {
			return runCommand(ctx)
		},
	}
	runtime, err := service.New(service.Config{Components: []service.Component{command}})
	if err != nil {
		log.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func runCommand(context.Context) error { return nil }
