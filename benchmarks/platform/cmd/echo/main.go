package main

import (
	"net/http"

	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/processcore"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
	"github.com/labstack/echo/v4"
)

//lint:ignore U1000 Called by build-tag-specific process entry points.
func run(options workload.Options) int { //nolint:unused // Called by tagged process entry points.
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		return 1
	}
	router := echo.New()
	router.HideBanner = true
	router.HidePort = true
	router.POST("/postal/search", func(ctx echo.Context) error {
		status, body := workload.PostalResponse(ctx.Request().Body)

		return ctx.Blob(status, "application/json", body)
	})
	router.POST("/track/ingest", func(ctx echo.Context) error {
		status, body := workload.TrackResponse(
			ctx.Request().Body,
			ctx.Request().Context(),
			factory,
			false,
		)

		return ctx.Blob(status, "application/json", body)
	})
	router.POST("/track/rpc", func(ctx echo.Context) error {
		status, body := workload.TrackResponse(
			ctx.Request().Body,
			ctx.Request().Context(),
			factory,
			true,
		)

		return ctx.Blob(status, "application/json", body)
	})
	router.POST("/location/lookup", func(ctx echo.Context) error {
		status, body := workload.LocationResponse(ctx.Request().Body)

		return ctx.Blob(status, "application/json", body)
	})
	router.POST(
		"/_benchmark/drain",
		echo.WrapHandler(http.HandlerFunc(workload.DrainHTTP)),
	)
	router.GET("/panic", func(echo.Context) error { panic("benchmark panic") })
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
