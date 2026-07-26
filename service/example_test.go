package service_test

import (
	"context"
	"fmt"

	"github.com/faustbrian/golib/pkg/service/service"
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
