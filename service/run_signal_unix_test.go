//go:build unix

package service_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/service"
)

func TestRunReceivesProcessSignal(t *testing.T) {
	if os.Getenv("GO_SERVICE_SIGNAL_HELPER") == "1" {
		runtime, err := service.New(service.Config{Components: []service.Component{{
			Name: "ready-barrier",
			Start: func(context.Context) error {
				fmt.Println("ready")

				return nil
			},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.Run(context.Background(), runtime, service.RunConfig{
			Signals:         []os.Signal{syscall.SIGUSR1},
			ShutdownTimeout: time.Second,
		}); err != nil {
			t.Fatal(err)
		}

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestRunReceivesProcessSignal$",
	)
	command.Env = append(os.Environ(), "GO_SERVICE_SIGNAL_HELPER=1")
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe() error = %v", err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatalf("helper readiness = %q, error = %v", scanner.Text(), scanner.Err())
	}
	if err := command.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatalf("Signal() error = %v", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("helper Wait() error = %v", err)
	}
}
