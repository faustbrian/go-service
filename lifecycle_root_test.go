package service_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/faustbrian/golib/pkg/service"
)

func TestRootLifecycleOwnsComponentsInDeclarationOrder(t *testing.T) {
	t.Parallel()

	var events []string
	component := func(name string) service.Component {
		return service.Component{
			Name: name,
			Start: func(context.Context) error {
				events = append(events, "start "+name)

				return nil
			},
			Stop: func(context.Context) error {
				events = append(events, "stop "+name)

				return nil
			},
		}
	}

	runtime, err := service.New(service.Config{
		Components: []service.Component{
			component("database"),
			component("worker"),
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	want := []string{
		"start database",
		"start worker",
		"stop worker",
		"stop database",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}
