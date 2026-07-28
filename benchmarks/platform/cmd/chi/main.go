package main

import (
	"net/http"

	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/processcore"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
	"github.com/go-chi/chi/v5"
)

//lint:ignore U1000 Called by build-tag-specific process entry points.
func run(options workload.Options) int { //nolint:unused // Called by tagged process entry points.
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		return 1
	}
	router := chi.NewRouter()
	router.Post("/postal/search", http.HandlerFunc(workload.PostalHTTP).ServeHTTP)
	router.Post("/track/ingest", workload.TrackHTTP(factory).ServeHTTP)
	router.Post("/track/rpc", workload.TrackRPCHTTP(factory).ServeHTTP)
	router.Post("/location/lookup", http.HandlerFunc(workload.LocationHTTP).ServeHTTP)
	router.Post("/_benchmark/drain", http.HandlerFunc(workload.DrainHTTP).ServeHTTP)
	router.Get("/panic", http.HandlerFunc(workload.PanicHTTP).ServeHTTP)
	handler, err := workload.Standard(
		router,
		factory,
		options,
	)
	if err != nil {
		return 1
	}

	return processcore.Main(handler)
}
