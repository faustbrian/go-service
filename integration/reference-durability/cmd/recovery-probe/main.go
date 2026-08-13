// Package main provides the disposable process boundary used by the local
// durability recovery campaign.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	referencedurability "github.com/faustbrian/golib/pkg/service/integration/reference-durability"
)

const expectationLimit = 4 << 10

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("recovery-probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var mode, databaseURL, valkeyAddress, stream, expectationPath string
	var timeout time.Duration
	flags.StringVar(&mode, "mode", "", "prepare, recover, or verify-ack")
	flags.StringVar(&databaseURL, "database-url", "", "disposable PostgreSQL connection URL")
	flags.StringVar(&valkeyAddress, "valkey-address", "", "disposable Valkey address")
	flags.StringVar(&stream, "stream", "", "task-owned Valkey Stream")
	flags.StringVar(&expectationPath, "expectation", "", "task-owned expectation JSON path")
	flags.DurationVar(&timeout, "timeout", 30*time.Second, "operation timeout")
	if err := flags.Parse(arguments); err != nil {
		return fmt.Errorf("recovery probe: parse flags: %w", err)
	}
	if flags.NArg() != 0 || timeout <= 0 {
		return errors.New("recovery probe: invalid arguments")
	}
	config := referencedurability.Config{
		DatabaseURL: databaseURL, ValkeyAddress: valkeyAddress, Stream: stream,
	}
	switch mode {
	case "prepare":
		if expectationPath == "" {
			return errors.New("recovery probe: prepare requires an expectation path")
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		session, err := referencedurability.PrepareRecovery(ctx, config)
		cancel()
		if err != nil {
			return err
		}
		defer func() { _ = session.Close() }()
		if err := writeExpectation(expectationPath, session.Expectation()); err != nil {
			return err
		}
		waitCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-waitCtx.Done()
		return session.Close()
	case "recover":
		if expectationPath == "" {
			return errors.New("recovery probe: recover requires an expectation path")
		}
		expectation, err := readExpectation(expectationPath)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		result, err := referencedurability.Recover(ctx, config, expectation)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	case "verify-ack":
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := referencedurability.VerifyRecoveryAcknowledgement(ctx, config); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]bool{"acknowledgement_persisted": true})
	default:
		return errors.New("recovery probe: mode must be prepare, recover, or verify-ack")
	}
}

func writeExpectation(path string, expectation referencedurability.RecoveryExpectation) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".recovery-expectation-*")
	if err != nil {
		return fmt.Errorf("recovery probe: create expectation: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if err != nil {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := json.NewEncoder(temporary).Encode(expectation); err != nil {
		return fmt.Errorf("recovery probe: encode expectation: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("recovery probe: sync expectation: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("recovery probe: close expectation: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("recovery probe: publish expectation: %w", err)
	}
	return nil
}

func readExpectation(path string) (referencedurability.RecoveryExpectation, error) {
	cleanPath := filepath.Clean(path)
	root, err := os.OpenRoot(filepath.Dir(cleanPath))
	if err != nil {
		return referencedurability.RecoveryExpectation{}, fmt.Errorf("recovery probe: open expectation directory: %w", err)
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(filepath.Base(cleanPath))
	if err != nil {
		return referencedurability.RecoveryExpectation{}, fmt.Errorf("recovery probe: open expectation: %w", err)
	}
	defer func() { _ = file.Close() }()
	decoder := json.NewDecoder(io.LimitReader(file, expectationLimit))
	decoder.DisallowUnknownFields()
	var expectation referencedurability.RecoveryExpectation
	if err := decoder.Decode(&expectation); err != nil {
		return referencedurability.RecoveryExpectation{}, fmt.Errorf("recovery probe: decode expectation: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return referencedurability.RecoveryExpectation{}, errors.New("recovery probe: expectation contains trailing data")
	}
	return expectation, nil
}
