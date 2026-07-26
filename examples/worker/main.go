package main

import (
	"context"
	"log"

	"github.com/faustbrian/golib/pkg/service/service"
)

func main() {
	runtime, err := service.New(service.Config{})
	if err != nil {
		log.Fatal(err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		log.Fatal(err)
	}
	if err := runtime.Go("consumer", func(ctx context.Context) error {
		<-ctx.Done()

		return nil
	}); err != nil {
		log.Fatal(err)
	}
	if err := service.Wait(context.Background(), runtime, service.RunConfig{}); err != nil {
		log.Fatal(err)
	}
}
