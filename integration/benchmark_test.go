package integration_test

import (
	"context"
	"testing"

	"github.com/faustbrian/golib/pkg/service/integration"
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

func TestHookAllocationBudget(t *testing.T) {
	component, err := integration.New("hook", integration.Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	allocations := testing.AllocsPerRun(100, func() {
		if err := component.Start(ctx); err != nil {
			panic(err)
		}
		if err := component.Stop(ctx); err != nil {
			panic(err)
		}
	})
	if allocations != 0 {
		t.Fatalf("allocations = %.1f, budget = 0", allocations)
	}
}
