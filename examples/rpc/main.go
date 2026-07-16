package main

import (
	"context"
	"log"

	"github.com/faustbrian/go-service/service"
)

func main() {
	rpc := service.Component{
		Name:  "rpc",
		Start: func(context.Context) error { return nil },
		Stop:  func(context.Context) error { return nil },
	}
	runtime, err := service.New(service.Config{Components: []service.Component{rpc}})
	if err != nil {
		log.Fatal(err)
	}
	if err := service.Run(context.Background(), runtime, service.RunConfig{}); err != nil {
		log.Fatal(err)
	}
}
