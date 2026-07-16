package integration_test

import (
	"context"
	"fmt"

	"github.com/faustbrian/go-service/integration"
)

func ExampleNew() {
	component, _ := integration.New("configuration", integration.Hooks{
		Start: func(context.Context) error {
			fmt.Println("load and validate configuration")

			return nil
		},
	})
	_ = component.Start(context.Background())
	// Output:
	// load and validate configuration
}
