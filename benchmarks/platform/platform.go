package platform

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/faustbrian/golib/pkg/correlation"
	httpcorrelation "github.com/faustbrian/golib/pkg/correlation/http"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	"github.com/gofiber/fiber/v3"
	"github.com/labstack/echo/v4"
)

// BodyLimit is the equivalent request-body boundary for every candidate.
const BodyLimit = workload.BodyLimit

// Trace derives caller-owned trace context without installing a global
// propagator.
type Trace func(context.Context) context.Context

// Options controls equivalent optional logging and tracing behavior.
type Options struct {
	// Logger enables one bounded completion log for each request.
	Logger *slog.Logger
	// Trace enables caller-owned context derivation after correlation.
	Trace Trace
}

// Endpoint executes one in-process HTTP request through a candidate runtime.
type Endpoint interface {
	// Do executes one prepared request through the candidate runtime.
	Do(*http.Request) (*http.Response, error)
	// Close releases resources owned by the endpoint.
	Close() error
}

// Candidate is one behavior-matched runtime implementation.
type Candidate struct {
	// Name is the stable benchmark label.
	Name string
	// IncompatibleRuntime identifies the separately disclosed fasthttp surface.
	IncompatibleRuntime bool
	// New constructs a fresh endpoint outside measured request work.
	New func(*correlation.Factory, Options) (Endpoint, error)
}

// Candidates returns the frozen comparison set in report order.
func Candidates() []Candidate {
	return []Candidate{
		{Name: "plain-net-http", New: newPlainEndpoint},
		{Name: "low-level-service", New: newLowLevelEndpoint},
		{Name: "cohesive-service", New: newCohesiveEndpoint},
		{Name: "chi", New: newChiEndpoint},
		{Name: "gin", New: newGinEndpoint},
		{Name: "echo", New: newEchoEndpoint},
		{
			Name:                "fiber-fasthttp",
			IncompatibleRuntime: true,
			New:                 newFiberEndpoint,
		},
	}
}

type handlerEndpoint struct {
	handler http.Handler
}

func (endpoint *handlerEndpoint) Do(request *http.Request) (*http.Response, error) {
	response := httptest.NewRecorder()
	endpoint.handler.ServeHTTP(response, request)

	return response.Result(), nil
}

func (*handlerEndpoint) Close() error { return nil }

func newPlainEndpoint(
	factory *correlation.Factory,
	options Options,
) (Endpoint, error) {
	factory, err := resolveFactory(factory)
	if err != nil {
		return nil, err
	}

	return newStandardEndpoint(newServeMux(factory), factory, options)
}

func newLowLevelEndpoint(
	factory *correlation.Factory,
	options Options,
) (Endpoint, error) {
	factory, err := resolveFactory(factory)
	if err != nil {
		return nil, err
	}
	handler, err := newServicePipeline(newServeMux(factory), factory, options)
	if err != nil {
		return nil, err
	}

	return &handlerEndpoint{handler: handler}, nil
}

func newCohesiveEndpoint(
	factory *correlation.Factory,
	options Options,
) (Endpoint, error) {
	factory, err := resolveFactory(factory)
	if err != nil {
		return nil, err
	}
	handler, err := newServicePipeline(newServeMux(factory), factory, options)
	if err != nil {
		return nil, err
	}

	return &handlerEndpoint{handler: handler}, nil
}

func newChiEndpoint(
	factory *correlation.Factory,
	options Options,
) (Endpoint, error) {
	factory, err := resolveFactory(factory)
	if err != nil {
		return nil, err
	}
	router := chi.NewRouter()
	router.Post("/postal/search", http.HandlerFunc(postalHTTP).ServeHTTP)
	router.Post("/track/ingest", workload.TrackHTTP(factory).ServeHTTP)
	router.Post("/track/rpc", workload.TrackRPCHTTP(factory).ServeHTTP)
	router.Post("/location/lookup", http.HandlerFunc(workload.LocationHTTP).ServeHTTP)
	router.Post("/_benchmark/drain", http.HandlerFunc(workload.DrainHTTP).ServeHTTP)
	router.Get("/panic", http.HandlerFunc(panicHTTP).ServeHTTP)

	return newStandardEndpoint(router, factory, options)
}

func newGinEndpoint(
	factory *correlation.Factory,
	options Options,
) (Endpoint, error) {
	factory, err := resolveFactory(factory)
	if err != nil {
		return nil, err
	}
	router := gin.New()
	router.POST("/postal/search", func(ctx *gin.Context) {
		status, body := postalResponse(ctx.Request.Body)
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

	return newStandardEndpoint(router, factory, options)
}

func newEchoEndpoint(
	factory *correlation.Factory,
	options Options,
) (Endpoint, error) {
	factory, err := resolveFactory(factory)
	if err != nil {
		return nil, err
	}
	router := echo.New()
	router.HideBanner = true
	router.HidePort = true
	router.POST("/postal/search", func(ctx echo.Context) error {
		status, body := postalResponse(ctx.Request().Body)

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

	return newStandardEndpoint(router, factory, options)
}

func newStandardEndpoint(
	router http.Handler,
	factory *correlation.Factory,
	options Options,
) (Endpoint, error) {
	factory, err := resolveFactory(factory)
	if err != nil {
		return nil, err
	}
	identity, err := httpcorrelation.New(factory, httpcorrelation.Options{})
	if err != nil {
		return nil, err
	}
	handler := requestBodyLimit(router)
	handler = optionalHTTP(handler, options)
	handler = identity.Wrap(handler)
	handler = recoverHTTP(handler)

	return &handlerEndpoint{handler: handler}, nil
}

func newServicePipeline(
	router http.Handler,
	factory *correlation.Factory,
	options Options,
) (http.Handler, error) {
	factory, err := resolveFactory(factory)
	if err != nil {
		return nil, err
	}
	identity, err := httpcorrelation.New(factory, httpcorrelation.Options{})
	if err != nil {
		return nil, err
	}
	limited, err := serverhttp.LimitBody(BodyLimit)
	if err != nil {
		return nil, err
	}
	middleware := []serverhttp.Middleware{
		serverhttp.Recover(),
		identity.Wrap,
	}
	if options.Trace != nil || options.Logger != nil {
		middleware = append(middleware, func(next http.Handler) http.Handler {
			return optionalHTTP(next, options)
		})
	}
	middleware = append(middleware, limited)

	return serverhttp.Chain(router, middleware...)
}

func optionalHTTP(next http.Handler, options Options) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if options.Trace != nil {
			request = request.WithContext(options.Trace(request.Context()))
		}
		started := time.Now()
		next.ServeHTTP(writer, request)
		if options.Logger != nil {
			options.Logger.InfoContext(
				request.Context(),
				"benchmark request",
				"method",
				request.Method,
				"elapsed",
				time.Since(started),
			)
		}
	})
}

func requestBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.ContentLength > BodyLimit {
			http.Error(writer, "request body too large", http.StatusRequestEntityTooLarge)

			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, BodyLimit)
		next.ServeHTTP(writer, request)
	})
}

func recoverHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recover() != nil {
				http.Error(
					writer,
					"internal server error",
					http.StatusInternalServerError,
				)
			}
		}()
		next.ServeHTTP(writer, request)
	})
}

func newServeMux(factory *correlation.Factory) http.Handler {
	router := http.NewServeMux()
	router.HandleFunc("POST /postal/search", postalHTTP)
	router.HandleFunc("POST /track/ingest", workload.TrackHTTP(factory))
	router.HandleFunc("POST /track/rpc", workload.TrackRPCHTTP(factory))
	router.HandleFunc("POST /location/lookup", workload.LocationHTTP)
	router.HandleFunc("POST /_benchmark/drain", workload.DrainHTTP)
	router.HandleFunc("GET /panic", panicHTTP)

	return router
}

func postalHTTP(writer http.ResponseWriter, request *http.Request) {
	workload.PostalHTTP(writer, request)
}

func panicHTTP(http.ResponseWriter, *http.Request) {
	panic("benchmark panic")
}

func postalResponse(body io.Reader) (int, []byte) {
	return workload.PostalResponse(body)
}

type fiberEndpoint struct {
	app *fiber.App
}

func (endpoint *fiberEndpoint) Do(request *http.Request) (*http.Response, error) {
	return endpoint.app.Test(request, fiber.TestConfig{Timeout: 5 * time.Second})
}

func (*fiberEndpoint) Close() error { return nil }

func newFiberEndpoint(
	factory *correlation.Factory,
	options Options,
) (Endpoint, error) {
	factory, err := resolveFactory(factory)
	if err != nil {
		return nil, err
	}
	app := fiber.New(fiber.Config{
		BodyLimit: BodyLimit + 1,
		ErrorHandler: func(ctx fiber.Ctx, _ error) error {
			return ctx.Status(http.StatusInternalServerError).
				SendString("internal server error")
		},
	})
	app.Use(func(ctx fiber.Ctx) (err error) {
		values, startErr := factory.Start()
		if startErr != nil {
			return startErr
		}
		ctx.Set(httpcorrelation.CorrelationHeader, values.CorrelationID.String())
		ctx.Set(httpcorrelation.RequestHeader, values.RequestID.String())
		ctx.SetContext(correlation.WithValues(ctx.Context(), values))
		if options.Trace != nil {
			ctx.SetContext(options.Trace(ctx.Context()))
		}
		started := time.Now()
		defer func() {
			if recover() != nil {
				err = ctx.Status(http.StatusInternalServerError).
					SendString("internal server error")
			}
			if options.Logger != nil {
				options.Logger.InfoContext(
					ctx.Context(),
					"benchmark request",
					"method",
					ctx.Method(),
					"elapsed",
					time.Since(started),
				)
			}
		}()
		if len(ctx.Req().BodyRaw()) > BodyLimit {
			return ctx.Status(http.StatusRequestEntityTooLarge).
				SendString("request body too large")
		}

		return ctx.Next()
	})
	app.Post("/postal/search", func(ctx fiber.Ctx) error {
		status, body := postalResponse(bytes.NewReader(ctx.Req().BodyRaw()))
		ctx.Set("Content-Type", "application/json")

		return ctx.Status(status).Send(body)
	})
	app.Post("/track/ingest", func(ctx fiber.Ctx) error {
		status, body := workload.TrackResponse(
			bytes.NewReader(ctx.Req().BodyRaw()),
			ctx.Context(),
			factory,
			false,
		)
		ctx.Set("Content-Type", "application/json")

		return ctx.Status(status).Send(body)
	})
	app.Post("/track/rpc", func(ctx fiber.Ctx) error {
		status, body := workload.TrackResponse(
			bytes.NewReader(ctx.Req().BodyRaw()),
			ctx.Context(),
			factory,
			true,
		)
		ctx.Set("Content-Type", "application/json")

		return ctx.Status(status).Send(body)
	})
	app.Post("/location/lookup", func(ctx fiber.Ctx) error {
		status, body := workload.LocationResponse(bytes.NewReader(ctx.Req().BodyRaw()))
		ctx.Set("Content-Type", "application/json")

		return ctx.Status(status).Send(body)
	})
	app.Post("/_benchmark/drain", func(ctx fiber.Ctx) error {
		workload.WaitForDrain(ctx.Context())

		return ctx.Status(http.StatusOK).JSON(map[string]bool{"drained": true})
	})
	app.Get("/panic", func(fiber.Ctx) error { panic("benchmark panic") })

	return &fiberEndpoint{app: app}, nil
}

func resolveFactory(
	factory *correlation.Factory,
) (*correlation.Factory, error) {
	if factory != nil {
		return factory, nil
	}

	return correlation.NewFactory(correlation.FactoryOptions{})
}
