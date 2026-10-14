package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/faustbrian/go-service/benchmarks/platform/internal/measure"
	"github.com/faustbrian/go-service/benchmarks/platform/internal/workload"
)

const rssInterval = 20 * time.Millisecond

type rssResult struct {
	peak     int64
	readings int
	err      error
}

// Stop cancels and joins the sampler even after a read failure. The initial
// observation is synchronous; measurement cannot silently continue without RSS.
func startRSSMonitor(interval time.Duration, read func() (int64, error)) (func() rssResult, error) {
	if interval <= 0 || read == nil {
		return nil, errors.New("invalid RSS sampler")
	}
	initial, err := read()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	results := make(chan rssResult, 1)
	go func() {
		result := rssResult{peak: initial, readings: 1}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		defer func() { results <- result }()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				value, readErr := read()
				if readErr != nil {
					result.err = readErr
					return
				}
				result.peak = max(result.peak, value)
				result.readings++
			}
		}
	}()
	var once sync.Once
	var result rssResult
	return func() rssResult {
		once.Do(func() { cancel(); result = <-results })
		return result
	}, nil
}

func parseSweep(value string) ([]int, error) {
	var result []int
	for _, part := range strings.Split(value, ",") {
		concurrency, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || concurrency < 1 || concurrency > 256 || len(result) >= 8 ||
			len(result) > 0 && concurrency <= result[len(result)-1] {
			return nil, errors.New("saturation sweep must contain 2-8 strictly increasing concurrency values in 1..256")
		}
		result = append(result, concurrency)
	}
	if len(result) < 2 {
		return nil, errors.New("saturation sweep requires at least two points")
	}
	return result, nil
}

func cancellationObservation(address string, mode string) (measure.Cancellation, error) {
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: processTimeout}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	trigger := time.Time{}
	if mode == "deadline" {
		cancel()
		trigger = time.Now().Add(150 * time.Millisecond)
		ctx, cancel = context.WithDeadline(context.Background(), trigger)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+"/_benchmark/cancel?mode="+mode, nil)
	if err != nil {
		return measure.Cancellation{}, err
	}
	done := make(chan error, 1)
	go func() {
		response, callErr := client.Do(request)
		if callErr == nil {
			_, callErr = io.Copy(io.Discard, response.Body)
			callErr = errors.Join(callErr, response.Body.Close())
		}
		done <- callErr
	}()
	// Every error path cancels and joins the HTTP caller before releasing its
	// transport. A timed-out status observation cannot leave an owned request.
	defer func() { cancel(); <-done }()
	status, err := waitCancellationStatus(client, address, mode, false)
	if err != nil {
		return measure.Cancellation{}, err
	}
	if status.Completed {
		return measure.Cancellation{}, errors.New("cancellation handler completed before client trigger")
	}
	if mode == "disconnect" {
		trigger = time.Now()
		cancel()
	}
	status, err = waitCancellationStatus(client, address, mode, true)
	if err != nil {
		return measure.Cancellation{}, err
	}
	result := measure.Cancellation{Mode: mode, Observed: status.Observed, WaitMilliseconds: status.WaitMilliseconds}
	if status.Observed {
		result.LatencyMilliseconds = milliseconds(time.Unix(0, status.ObservedUnixNano).Sub(trigger))
		if result.LatencyMilliseconds < 0 {
			return measure.Cancellation{}, errors.New("server cancellation preceded recorded trigger")
		}
	}
	return result, nil
}

func waitCancellationStatus(client *http.Client, address string, mode string, completed bool) (workload.CancellationStatus, error) {
	deadline := time.Now().Add(processTimeout)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/_benchmark/cancel/status?mode="+mode, nil)
		if err != nil {
			return workload.CancellationStatus{}, err
		}
		response, err := client.Do(request)
		if err != nil {
			return workload.CancellationStatus{}, err
		}
		var status workload.CancellationStatus
		decodeErr := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&status)
		closeErr := response.Body.Close()
		if response.StatusCode != http.StatusOK || decodeErr != nil || closeErr != nil {
			return workload.CancellationStatus{}, fmt.Errorf("cancellation status %d: %w", response.StatusCode, errors.Join(decodeErr, closeErr))
		}
		if status.Started && (!completed || status.Completed) {
			return status, nil
		}
		time.Sleep(time.Millisecond)
	}
	return workload.CancellationStatus{}, errors.New("cancellation observation did not complete")
}

// This replaces the removed repository-wide digest script at the existing
// benchmark reuse boundary. Hash measurement source, locks and build settings;
// documentation/results and unrelated root source do not invalidate a capture
// consuming the explicitly released service dependency.
func benchmarkInputDigest(directory string) (string, error) {
	hash := sha256.New()
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer func() { _ = root.Close() }()
	err = filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != directory && (entry.Name() == "artifacts" || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") && entry.Name() != "benchmark_test.go" {
			return nil
		}
		if filepath.Ext(path) != ".go" && entry.Name() != "go.mod" && entry.Name() != "go.sum" && entry.Name() != "Makefile" {
			return nil
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		document, err := root.ReadFile(relative)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", relative, len(document))
		_, _ = hash.Write(document)
		return nil
	})
	return hex.EncodeToString(hash.Sum(nil)), err
}

func validateAdditionalObservations(rawDirectory string, prefix string, sample measure.Sample) error {
	path := filepath.Join(rawDirectory, prefix+"-observations.json")
	digest, err := fileDigest(path)
	if err != nil {
		return err
	}
	if sample.ObservationsRaw != path+"#sha256="+digest {
		return errors.New("checkpoint additional observation digest differs")
	}
	// #nosec G304 -- The path is derived from the fixed sample prefix under the trusted root.
	document, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var observations measure.Observations
	if err := json.Unmarshal(document, &observations); err != nil {
		return err
	}
	if !reflect.DeepEqual(sample.Observations, observations) || observations.PeakRSSBytes < sample.IdleRSSBytes || observations.RSSReadings < 1 {
		return errors.New("checkpoint additional observations differ from raw evidence")
	}
	if len(sample.Cancellation) != 2 || sample.Cancellation[0].Mode != "disconnect" || sample.Cancellation[1].Mode != "deadline" {
		return errors.New("checkpoint cancellation observations missing")
	}
	for _, observation := range sample.Cancellation {
		if observation.WaitMilliseconds <= 0 || math.IsNaN(observation.WaitMilliseconds) || math.IsInf(observation.WaitMilliseconds, 0) || observation.LatencyMilliseconds < 0 || math.IsNaN(observation.LatencyMilliseconds) || math.IsInf(observation.LatencyMilliseconds, 0) || !observation.Observed && observation.LatencyMilliseconds != 0 {
			return errors.New("checkpoint cancellation timing invalid")
		}
	}
	if len(sample.Saturation) < 2 {
		return errors.New("checkpoint saturation observations missing")
	}
	if len(sample.SaturationRaw) != len(sample.Saturation) {
		return errors.New("checkpoint saturation artifacts missing")
	}
	for index, step := range sample.Saturation {
		path := filepath.Join(rawDirectory, fmt.Sprintf("%s-saturation-%03d.json", prefix, step.Concurrency))
		digest, err := fileDigest(path)
		if err != nil {
			return err
		}
		if sample.SaturationRaw[index] != path+"#sha256="+digest {
			return errors.New("checkpoint saturation digest differs")
		}
		// #nosec G304 -- The trusted checkpoint root and bounded concurrency derive this path.
		document, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		load, err := measure.ParseOHA(document)
		if err != nil || load != step.Load {
			return errors.New("checkpoint saturation metrics differ from raw evidence")
		}
	}
	return nil
}
