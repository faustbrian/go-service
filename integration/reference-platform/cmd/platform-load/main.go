package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

var errInvalidConfig = errors.New("platform load configuration is invalid")

type config struct {
	Endpoint          string
	ResourcesEndpoint string
	Requests          int
	Concurrency       int
	RequestTimeout    time.Duration
	SampleInterval    time.Duration
}

type resourceReport struct {
	HeapAllocBytes      uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes        uint64 `json:"heap_sys_bytes"`
	Goroutines          int    `json:"goroutines"`
	OpenFileDescriptors int    `json:"open_file_descriptors"`
}

type report struct {
	Requested              int           `json:"requested"`
	Completed              int           `json:"completed"`
	Failed                 int           `json:"failed"`
	SampleFailures         int           `json:"sample_failures"`
	MaxInFlight            int64         `json:"max_in_flight"`
	Duration               time.Duration `json:"duration_nanoseconds"`
	RequestsPerSecond      float64       `json:"requests_per_second"`
	P50                    time.Duration `json:"p50_nanoseconds"`
	P95                    time.Duration `json:"p95_nanoseconds"`
	P99                    time.Duration `json:"p99_nanoseconds"`
	MaxHeapAllocBytes      uint64        `json:"max_heap_alloc_bytes"`
	MaxHeapSysBytes        uint64        `json:"max_heap_sys_bytes"`
	MaxGoroutines          int64         `json:"max_goroutines"`
	MaxOpenFileDescriptors int64         `json:"max_open_file_descriptors"`
}

func main() {
	var settings config
	var overallTimeout time.Duration
	flag.StringVar(&settings.Endpoint, "endpoint", "", "equivalent business endpoint")
	flag.StringVar(&settings.ResourcesEndpoint, "resources-endpoint", "", "process resource endpoint")
	flag.IntVar(&settings.Requests, "requests", 20_000, "total requests")
	flag.IntVar(&settings.Concurrency, "concurrency", 16, "maximum concurrent requests")
	flag.DurationVar(&settings.RequestTimeout, "request-timeout", 2*time.Second, "per-request timeout")
	flag.DurationVar(&settings.SampleInterval, "sample-interval", 10*time.Millisecond, "resource sample interval")
	flag.DurationVar(&overallTimeout, "overall-timeout", 2*time.Minute, "complete campaign timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()
	result, err := run(ctx, settings)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, settings config) (report, error) {
	if ctx == nil || settings.Requests < 1 || settings.Concurrency < 1 ||
		settings.Concurrency > settings.Requests || settings.RequestTimeout <= 0 ||
		settings.SampleInterval <= 0 || !validEndpoint(settings.Endpoint) ||
		!validEndpoint(settings.ResourcesEndpoint) {
		return report{}, errInvalidConfig
	}
	if err := ctx.Err(); err != nil {
		return report{}, err
	}

	transport := &http.Transport{
		MaxIdleConns: settings.Concurrency + 2, MaxIdleConnsPerHost: settings.Concurrency + 2,
		MaxConnsPerHost: settings.Concurrency + 2, IdleConnTimeout: 30 * time.Second,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	result := report{Requested: settings.Requests}
	var resource resourceMaxima
	if err := sampleResources(ctx, client, settings, &resource); err != nil {
		return report{}, fmt.Errorf("initial resource sample: %w", err)
	}

	sampleCtx, stopSampling := context.WithCancel(ctx)
	var sampler sync.WaitGroup
	sampler.Add(1)
	go func() {
		defer sampler.Done()
		ticker := time.NewTicker(settings.SampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-sampleCtx.Done():
				return
			case <-ticker.C:
				if err := sampleResources(sampleCtx, client, settings, &resource); err != nil && sampleCtx.Err() == nil {
					resource.sampleFailures.Add(1)
				}
			}
		}
	}()

	jobs := make(chan struct{})
	latencies := make(chan time.Duration, settings.Requests)
	var workers sync.WaitGroup
	var completed, failed, inFlight, maxInFlight atomic.Int64
	workers.Add(settings.Concurrency)
	start := time.Now()
	for range settings.Concurrency {
		go func() {
			defer workers.Done()
			for range jobs {
				current := inFlight.Add(1)
				updateMaximum(&maxInFlight, current)
				requestStart := time.Now()
				err := performRequest(ctx, client, settings)
				latencies <- time.Since(requestStart)
				inFlight.Add(-1)
				if err != nil {
					failed.Add(1)
				} else {
					completed.Add(1)
				}
			}
		}()
	}
	for range settings.Requests {
		select {
		case jobs <- struct{}{}:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			stopSampling()
			sampler.Wait()
			return report{}, ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	duration := time.Since(start)
	stopSampling()
	sampler.Wait()
	close(latencies)

	values := make([]time.Duration, 0, settings.Requests)
	for latency := range latencies {
		values = append(values, latency)
	}
	slices.Sort(values)
	result.Completed = int(completed.Load())
	result.Failed = int(failed.Load())
	result.SampleFailures = int(resource.sampleFailures.Load())
	result.MaxInFlight = maxInFlight.Load()
	result.Duration = duration
	result.RequestsPerSecond = float64(len(values)) / duration.Seconds()
	result.P50 = percentile(values, 50)
	result.P95 = percentile(values, 95)
	result.P99 = percentile(values, 99)
	result.MaxHeapAllocBytes = resource.heapAlloc.Load()
	result.MaxHeapSysBytes = resource.heapSys.Load()
	result.MaxGoroutines = resource.goroutines.Load()
	result.MaxOpenFileDescriptors = resource.descriptors.Load()
	return result, nil
}

type resourceMaxima struct {
	heapAlloc      atomic.Uint64
	heapSys        atomic.Uint64
	goroutines     atomic.Int64
	descriptors    atomic.Int64
	sampleFailures atomic.Int64
}

func sampleResources(ctx context.Context, client *http.Client, settings config, maxima *resourceMaxima) error {
	requestCtx, cancel := context.WithTimeout(ctx, settings.RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, settings.ResourcesEndpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("resource status %d", response.StatusCode)
	}
	var sample resourceReport
	decoder := json.NewDecoder(io.LimitReader(response.Body, 4<<10))
	if err := decoder.Decode(&sample); err != nil {
		return err
	}
	updateMaximumUint(&maxima.heapAlloc, sample.HeapAllocBytes)
	updateMaximumUint(&maxima.heapSys, sample.HeapSysBytes)
	updateMaximum(&maxima.goroutines, int64(sample.Goroutines))
	updateMaximum(&maxima.descriptors, int64(sample.OpenFileDescriptors))
	return nil
}

func performRequest(ctx context.Context, client *http.Client, settings config) error {
	requestCtx, cancel := context.WithTimeout(ctx, settings.RequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, settings.Endpoint, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<10))
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || !bytes.Equal(body, []byte("reference-platform\n")) {
		return fmt.Errorf("unexpected response status=%d body=%q", response.StatusCode, body)
	}
	return nil
}

func validEndpoint(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed != nil &&
		(parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func percentile(values []time.Duration, percentile int) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := (len(values)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	return values[index-1]
}

func updateMaximum(target *atomic.Int64, candidate int64) {
	for current := target.Load(); candidate > current; current = target.Load() {
		if target.CompareAndSwap(current, candidate) {
			return
		}
	}
}

func updateMaximumUint(target *atomic.Uint64, candidate uint64) {
	for current := target.Load(); candidate > current; current = target.Load() {
		if target.CompareAndSwap(current, candidate) {
			return
		}
	}
}
