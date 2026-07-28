package main

import (
	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/processcore"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
)

//lint:ignore U1000 Called by build-tag-specific process entry points.
func run(options workload.Options) int { //nolint:unused // Called by tagged process entry points.
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		return 1
	}
	handler, err := workload.Standard(
		workload.NewMux(factory),
		factory,
		options,
	)
	if err != nil {
		return 1
	}

	return processcore.Main(handler)
}
