package integration_test

import (
	"context"
	"testing"

	"github.com/faustbrian/go-service/integration"
)

func BenchmarkHooks(benchmark *testing.B) {
	component, err := integration.New("hook", integration.Hooks{})
	if err != nil {
		benchmark.Fatal(err)
	}
	ctx := context.Background()

	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for range benchmark.N {
		if err := component.Start(ctx); err != nil {
			benchmark.Fatal(err)
		}
		if err := component.Stop(ctx); err != nil {
			benchmark.Fatal(err)
		}
	}
}
