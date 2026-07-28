//go:build benchmark_disabled

package main

import (
	"os"

	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
)

func main() { os.Exit(run(workload.DisabledOptions())) }
