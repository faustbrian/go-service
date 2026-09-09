package referencerecipes_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/faustbrian/go-correlation"
	queueservice "github.com/faustbrian/go-queue/adapters/service"
	queuecore "github.com/faustbrian/go-queue/core"
	"github.com/faustbrian/go-queue/job"
	"github.com/faustbrian/go-service"
	"github.com/faustbrian/go-service/healthhttp"
	"github.com/faustbrian/go-service/serverhttp"
)

func TestMinimalHTTPRecipeStartsServesReadinessAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	businessListener := listenLoopback(t)
	managementListener := listenLoopback(t)
	runtime, err := service.New(service.Config{})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	probes, err := healthhttp.New(healthhttp.Config{Lifecycle: runtime})
	if err != nil {
		t.Fatalf("healthhttp.New() error = %v", err)
	}
	business, err := serverhttp.New(businessListener, http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = io.WriteString(writer, "hello\n")
	}))
	if err != nil {
		t.Fatalf("serverhttp.New(business) error = %v", err)
	}
	managementMux := http.NewServeMux()
	managementMux.Handle("GET /readyz", probes.Readiness())
	management, err := serverhttp.New(managementListener, managementMux)
	if err != nil {
		t.Fatalf("serverhttp.New(management) error = %v", err)
	}

	if err = runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownContext, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = runtime.Shutdown(shutdownContext)
	})
	if err = runtime.Go("business-http", business.Run); err != nil {
		t.Fatalf("Go(business-http) error = %v", err)
	}
	if err = runtime.Go("management-http", management.Run); err != nil {
		t.Fatalf("Go(management-http) error = %v", err)
	}

	assertHTTPResponse(t, ctx, "http://"+businessListener.Addr().String()+"/", http.StatusOK, "hello\n")
	assertHTTPResponse(t, ctx, "http://"+managementListener.Addr().String()+"/readyz", http.StatusOK, "")
	if err = runtime.Drain(); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	assertHTTPResponse(t, ctx, "http://"+managementListener.Addr().String()+"/readyz", http.StatusServiceUnavailable, "")
	if err = runtime.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if runtime.State() != service.StateStopped {
		t.Fatalf("State() = %s, want stopped", runtime.State())
	}
}

func TestIngesterProcessorRecipeHandsOffAcknowledgesDrainsAndShutsDown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	broker := newTestBroker(1)
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		t.Fatalf("correlation.NewFactory() error = %v", err)
	}
	type handledTask struct {
		payload     string
		correlation correlation.Values
	}
	processed := make(chan handledTask, 1)
	processingCompleted := make(chan struct{})
	releaseProcessing := make(chan struct{})
	processor, err := queueservice.NewLifecycleWorker(
		queueservice.LifecycleWorkerOptions[*testBroker]{
			Name:            "processor",
			Resource:        broker,
			Correlation:     factory,
			TrustedMetadata: true,
			CloseAdmission:  (*testBroker).closeAdmission,
			Run: func(
				ctx context.Context,
				resource *testBroker,
				handler queueservice.Handler,
			) error {
				return resource.run(ctx, handler)
			},
			Shutdown: func(ctx context.Context, resource *testBroker) error {
				return resource.shutdown(ctx)
			},
			Handler: func(handlerContext context.Context, delivery queuecore.TaskMessage) error {
				values, ok := correlation.FromContext(handlerContext)
				if !ok {
					return errors.New("processor correlation metadata is missing")
				}
				processed <- handledTask{payload: string(delivery.Payload()), correlation: values}

				select {
				case <-releaseProcessing:
					close(processingCompleted)
					return nil
				case <-handlerContext.Done():
					return context.Cause(handlerContext)
				}
			},
		},
	)
	if err != nil {
		t.Fatalf("queueservice.NewLifecycleWorker() error = %v", err)
	}
	ingester, err := queueservice.NewProducer(
		queueservice.ProducerOptions[*testBroker]{
			Name:        "ingester",
			Resource:    broker,
			Correlation: factory,
			PublishWithAcceptance: func(
				ctx context.Context,
				resource *testBroker,
				message queuecore.QueuedMessage,
				options ...job.AllowOption,
			) (queueservice.PublishAcceptance, error) {
				return resource.publish(ctx, message, options...)
			},
		},
	)
	if err != nil {
		t.Fatalf("queueservice.NewProducer() error = %v", err)
	}
	processorPlan := processor.Plan()
	runtime, err := service.New(service.Config{Components: append(
		processorPlan.Components,
		ingester.Component(),
	)})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	if err = runtime.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownContext, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = runtime.Shutdown(shutdownContext)
	})
	for _, task := range processorPlan.Tasks {
		if err = runtime.Go(task.Name, task.Run); err != nil {
			t.Fatalf("Go(%s) error = %v", task.Name, err)
		}
	}

	parent, err := factory.Create()
	if err != nil {
		t.Fatalf("correlation.Factory.Create() error = %v", err)
	}
	publishContext := correlation.WithValues(ctx, parent)
	published, acceptance, publishErr := ingester.PublishWithAcceptance(
		publishContext,
		testPayload("shipment-42"),
	)
	if publishErr != nil || acceptance != queueservice.PublishAccepted {
		t.Fatalf("PublishWithAcceptance() = (%d, %v), want accepted", acceptance, publishErr)
	}
	select {
	case handled := <-processed:
		if handled.payload != "shipment-42" {
			t.Fatalf("processed payload = %q", handled.payload)
		}
		if handled.correlation.CorrelationID != published.CorrelationID ||
			handled.correlation.CausationID != correlation.CausationID(published.RequestID) ||
			handled.correlation.RequestID == "" ||
			handled.correlation.RequestID == published.RequestID {
			t.Fatalf(
				"processor correlation = %#v, want correlation %q caused by %q with a new request",
				handled.correlation,
				published.CorrelationID,
				published.RequestID,
			)
		}
	case <-ctx.Done():
		t.Fatal("processor did not receive the accepted handoff")
	}
	select {
	case <-broker.acknowledged:
		t.Fatal("processor acknowledged after decode but before application processing completed")
	default:
	}
	if err = runtime.Drain(); err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
	if _, acceptance, publishErr := ingester.PublishWithAcceptance(
		publishContext,
		testPayload("late"),
	); !errors.Is(publishErr, queueservice.ErrUnavailable) ||
		acceptance != queueservice.PublishNotAccepted {
		t.Fatalf("publish after Drain() = (%d, %v), want not accepted", acceptance, publishErr)
	}
	if broker.accepting.Load() {
		t.Fatal("Drain() did not withdraw processor intake")
	}
	select {
	case <-processingCompleted:
		t.Fatal("admitted application work completed before its release")
	default:
	}
	select {
	case <-broker.acknowledged:
		t.Fatal("processor acknowledged during drain before application processing completed")
	default:
	}
	close(releaseProcessing)
	select {
	case <-processingCompleted:
	case <-ctx.Done():
		t.Fatal("admitted application work did not complete during drain")
	}
	select {
	case <-broker.acknowledged:
	case <-ctx.Done():
		t.Fatal("processor did not acknowledge the accepted delivery during drain")
	}
	if err = runtime.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	select {
	case <-broker.stopped:
	default:
		t.Fatal("processor resource was not shut down")
	}
}
