package service_test

import (
	"context"
	"testing"

	"github.com/faustbrian/golib/pkg/service/service"
)

func BenchmarkStartShutdown(benchmark *testing.B) {
	ctx := context.Background()
	config := service.Config{Components: []service.Component{{Name: "worker"}}}

	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for range benchmark.N {
		runtime, err := service.New(config)
		if err != nil {
			benchmark.Fatal(err)
		}
		if err := runtime.Start(ctx); err != nil {
			benchmark.Fatal(err)
		}
		if err := runtime.Shutdown(ctx); err != nil {
			benchmark.Fatal(err)
		}
	}
}

func TestStartShutdownAllocationBudget(t *testing.T) {
	ctx := context.Background()
	config := service.Config{Components: []service.Component{{Name: "worker"}}}
	allocations := testing.AllocsPerRun(100, func() {
		runtime, err := service.New(config)
		if err != nil {
			panic(err)
		}
		if err := runtime.Start(ctx); err != nil {
			panic(err)
		}
		if err := runtime.Shutdown(ctx); err != nil {
			panic(err)
		}
	})
	if allocations > 10 {
		t.Fatalf("allocations = %.1f, budget = 10", allocations)
	}
}
