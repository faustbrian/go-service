//go:build benchmark_tracing

package main

import (
	"os"

	"github.com/faustbrian/go-service/benchmarks/platform/internal/workload"
)

func main() { os.Exit(run(workload.TracingOptions())) }
