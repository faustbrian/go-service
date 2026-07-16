package service_test

import (
	"context"
	"testing"

	"github.com/faustbrian/go-service/service"
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
