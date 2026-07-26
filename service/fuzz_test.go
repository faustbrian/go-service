package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/service"
)

func FuzzConfig(fuzz *testing.F) {
	fuzz.Add("worker", int64(0), 0)
	fuzz.Add("", int64(-1), -1)
	fuzz.Add(" worker ", int64(1), 4097)

	fuzz.Fuzz(func(t *testing.T, name string, timeoutNanoseconds int64, maxTasks int) {
		if timeoutNanoseconds > int64(time.Hour) {
			timeoutNanoseconds = int64(time.Hour)
		}
		if timeoutNanoseconds < -int64(time.Hour) {
			timeoutNanoseconds = -int64(time.Hour)
		}

		runtime, err := service.New(service.Config{
			Components:      []service.Component{{Name: name}},
			RollbackTimeout: time.Duration(timeoutNanoseconds),
			MaxTasks:        maxTasks,
		})
		if err != nil {
			return
		}
		if err := runtime.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	})
}
