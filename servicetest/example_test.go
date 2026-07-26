package servicetest_test

import (
	"context"
	"fmt"

	"github.com/faustbrian/golib/pkg/service/servicetest"
)

func ExampleBarrier() {
	var barrier servicetest.Barrier
	result := make(chan error, 1)
	go func() { result <- barrier.Wait(context.Background()) }()
	<-barrier.Entered()
	fmt.Println("entered")
	barrier.Release()
	fmt.Println(<-result)
	// Output:
	// entered
	// <nil>
}
