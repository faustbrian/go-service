package main

import (
	"net/http"

	"github.com/faustbrian/golib/pkg/correlation"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/processcore"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
	"github.com/gin-gonic/gin"
)

//lint:ignore U1000 Called by build-tag-specific process entry points.
func run(options workload.Options) int { //nolint:unused // Called by tagged process entry points.
	gin.SetMode(gin.ReleaseMode)
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		return 1
	}
	router := gin.New()
	router.POST("/postal/search", func(ctx *gin.Context) {
		status, body := workload.PostalResponse(ctx.Request.Body)
		ctx.Data(status, "application/json", body)
	})
	router.POST("/track/ingest", func(ctx *gin.Context) {
		status, body := workload.TrackResponse(
			ctx.Request.Body,
			ctx.Request.Context(),
			factory,
			false,
		)
		ctx.Data(status, "application/json", body)
	})
	router.POST("/track/rpc", func(ctx *gin.Context) {
		status, body := workload.TrackResponse(
			ctx.Request.Body,
			ctx.Request.Context(),
			factory,
			true,
		)
		ctx.Data(status, "application/json", body)
	})
	router.POST("/location/lookup", func(ctx *gin.Context) {
		status, body := workload.LocationResponse(ctx.Request.Body)
		ctx.Data(status, "application/json", body)
	})
	router.POST("/_benchmark/drain", gin.WrapH(http.HandlerFunc(workload.DrainHTTP)))
	router.GET("/panic", func(*gin.Context) { panic("benchmark panic") })
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
