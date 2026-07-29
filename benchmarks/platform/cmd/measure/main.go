package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/measure"
	"github.com/faustbrian/golib/pkg/service/benchmarks/platform/internal/workload"
)

const (
	benchmarkTimeout = 10 * time.Second
	processTimeout   = 3 * time.Second
)

type candidate struct {
	name         string
	command      string
	arguments    []string
	incompatible bool
}

type configuration struct {
	Samples                             int      `json:"samples"`
	Requests                            int      `json:"requests"`
	ProbeRequests                       int      `json:"probe_requests"`
	Concurrency                         int      `json:"concurrency"`
	ConfiguredDrainDeadlineMilliseconds float64  `json:"configured_drain_deadline_milliseconds"`
	Candidates                          []string `json:"candidates"`
	States                              []string `json:"states"`
}

type environment struct {
	OS               string `json:"os"`
	Architecture     string `json:"architecture"`
	LogicalCPUs      int    `json:"logical_cpus"`
	GoVersion        string `json:"go_version"`
	OHAVersion       string `json:"oha_version"`
	Kernel           string `json:"kernel"`
	SourceRevision   string `json:"source_revision"`
	GateInputDigest  string `json:"gate_input_digest"`
	ExecutionStarted string `json:"execution_started"`
}

type candidateResult struct {
	Candidate           string           `json:"candidate"`
	State               string           `json:"state"`
	IncompatibleRuntime bool             `json:"incompatible_runtime"`
	BinaryBytes         int64            `json:"binary_bytes"`
	BinarySHA256        string           `json:"binary_sha256"`
	Samples             []measure.Sample `json:"samples"`
	Summary             measure.Summary  `json:"summary"`
}

type budgetResult struct {
	Passed   bool     `json:"passed"`
	Failures []string `json:"failures"`
}

type report struct {
	Schema      string            `json:"schema"`
	Environment environment       `json:"environment"`
	Config      configuration     `json:"configuration"`
	Results     []candidateResult `json:"results"`
	Budgets     budgetResult      `json:"budgets"`
}

type flags struct {
	output        string
	samples       int
	requests      int
	probeRequests int
	concurrency   int
	candidates    string
	states        string
	enforce       bool
}

type preparedCandidate struct {
	item        candidate
	state       string
	binary      string
	resultIndex int
}

type measurementStep struct {
	CandidateIndex int
	SampleIndex    int
}

func main() {
	settings := parseFlags()
	if err := run(settings); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseFlags() flags {
	var settings flags
	flag.StringVar(&settings.output, "output", "artifacts/platform-process", "artifact directory")
	flag.IntVar(&settings.samples, "samples", 5, "independent process samples")
	flag.IntVar(&settings.requests, "requests", 100_000, "requests per business workload sample")
	flag.IntVar(&settings.probeRequests, "probe-requests", 20_000, "probe requests per sample")
	flag.IntVar(&settings.concurrency, "concurrency", 16, "oha connections")
	flag.StringVar(
		&settings.candidates,
		"candidates",
		"plain-net-http,low-level-service,cohesive-service,chi,gin,echo,fiber-fasthttp",
		"comma-separated candidate labels",
	)
	flag.StringVar(
		&settings.states,
		"states",
		"disabled,logging,tracing",
		"comma-separated middleware states",
	)
	flag.BoolVar(&settings.enforce, "enforce", true, "fail when service budgets fail")
	flag.Parse()

	return settings
}

func run(settings flags) error {
	if settings.samples < 1 || settings.requests < 1 ||
		settings.probeRequests < 1 || settings.concurrency < 1 {
		return errors.New("sample, request, probe, and concurrency values must be positive")
	}
	selectedCandidates, err := selectCandidates(split(settings.candidates))
	if err != nil {
		return err
	}
	states := split(settings.states)
	for _, state := range states {
		if state != "disabled" && state != "logging" && state != "tracing" {
			return fmt.Errorf("unknown state %q", state)
		}
	}
	if _, err := exec.LookPath("oha"); err != nil {
		return errors.New("oha is required")
	}
	if err := os.MkdirAll(settings.output, 0o750); err != nil {
		return err
	}
	binaryDirectory := filepath.Join(settings.output, "binaries")
	rawDirectory := filepath.Join(settings.output, "raw")
	if err := os.MkdirAll(binaryDirectory, 0o750); err != nil {
		return err
	}
	if err := os.MkdirAll(rawDirectory, 0o750); err != nil {
		return err
	}
	currentEnvironment, err := captureEnvironment()
	if err != nil {
		return err
	}
	currentReport := report{
		Schema:      "service-platform-process-benchmark/v2",
		Environment: currentEnvironment,
		Config: configuration{
			Samples:       settings.samples,
			Requests:      settings.requests,
			ProbeRequests: settings.probeRequests,
			Concurrency:   settings.concurrency,
			ConfiguredDrainDeadlineMilliseconds: milliseconds(
				workload.ShutdownTimeout,
			),
			Candidates: split(settings.candidates),
			States:     states,
		},
		Budgets: budgetResult{Passed: false},
	}
	preparedByState := make(map[string][]preparedCandidate, len(states))
	for _, item := range selectedCandidates {
		for _, state := range states {
			binary := filepath.Join(binaryDirectory, item.command+"-"+state)
			if err := buildCandidate(item, binary, state); err != nil {
				return err
			}
			binaryInfo, err := os.Stat(binary)
			if err != nil {
				return err
			}
			binaryDigest, err := fileDigest(binary)
			if err != nil {
				return err
			}
			result := candidateResult{
				Candidate:           item.name,
				State:               state,
				IncompatibleRuntime: item.incompatible,
				BinaryBytes:         binaryInfo.Size(),
				BinarySHA256:        binaryDigest,
				Samples:             make([]measure.Sample, 0, settings.samples),
			}
			currentReport.Results = append(currentReport.Results, result)
			resultIndex := len(currentReport.Results) - 1
			preparedByState[state] = append(
				preparedByState[state],
				preparedCandidate{
					item: item, state: state, binary: binary, resultIndex: resultIndex,
				},
			)
		}
	}
	for _, state := range states {
		prepared := preparedByState[state]
		for _, entry := range prepared {
			if err := warmCandidate(entry.item, entry.binary); err != nil {
				return err
			}
		}
		for _, step := range measurementOrder(len(prepared), settings.samples) {
			entry := prepared[step.CandidateIndex]
			sample, sampleErr := runSample(
				entry.item,
				entry.binary,
				entry.state,
				settings,
				rawDirectory,
				step.SampleIndex+1,
			)
			if sampleErr != nil {
				return sampleErr
			}
			currentReport.Results[entry.resultIndex].Samples = append(
				currentReport.Results[entry.resultIndex].Samples,
				sample,
			)
			currentReport.Results[entry.resultIndex].Summary = measure.Summarize(
				currentReport.Results[entry.resultIndex].Samples,
			)
			if err := writeReport(settings.output, currentReport); err != nil {
				return err
			}
		}
	}
	currentReport.Budgets = assess(currentReport.Results)
	if err := writeReport(settings.output, currentReport); err != nil {
		return err
	}
	if settings.enforce && !currentReport.Budgets.Passed {
		return fmt.Errorf(
			"frozen service budgets failed; inspect %s",
			filepath.Join(settings.output, "report.json"),
		)
	}

	return nil
}

func measurementOrder(candidateCount int, samples int) []measurementStep {
	steps := make([]measurementStep, 0, candidateCount*samples)
	for sampleIndex := range samples {
		if sampleIndex%2 == 0 {
			for candidateIndex := range candidateCount {
				steps = append(steps, measurementStep{
					CandidateIndex: candidateIndex,
					SampleIndex:    sampleIndex,
				})
			}

			continue
		}
		for candidateIndex := candidateCount - 1; candidateIndex >= 0; candidateIndex-- {
			steps = append(steps, measurementStep{
				CandidateIndex: candidateIndex,
				SampleIndex:    sampleIndex,
			})
		}
	}

	return steps
}

func warmCandidate(item candidate, binary string) error {
	businessAddress, err := availableAddress()
	if err != nil {
		return err
	}
	managementAddress, err := availableAddress()
	if err != nil {
		return err
	}
	//nolint:gosec // Binary and arguments come from the closed candidate registry.
	command := exec.CommandContext(context.Background(), binary, item.arguments...)
	command.Env = append(
		os.Environ(),
		"BENCH_BUSINESS_ADDRESS="+businessAddress,
		"BENCH_MANAGEMENT_ADDRESS="+managementAddress,
	)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return err
	}
	running := true
	defer func() {
		if running {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	if err := waitForProbe("http://" + managementAddress + "/startupz"); err != nil {
		return fmt.Errorf("%s warmup: %w", item.name, err)
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case waitErr := <-wait:
		if err := expectedSignalExit(waitErr); err != nil {
			return fmt.Errorf("%s warmup: %w", item.name, err)
		}
	case <-time.After(processTimeout):
		return fmt.Errorf("%s warmup shutdown exceeded %s", item.name, processTimeout)
	}
	running = false

	return nil
}

func runSample(
	item candidate,
	binary string,
	state string,
	settings flags,
	rawDirectory string,
	sampleNumber int,
) (measure.Sample, error) {
	businessAddress, err := availableAddress()
	if err != nil {
		return measure.Sample{}, err
	}
	managementAddress, err := availableAddress()
	if err != nil {
		return measure.Sample{}, err
	}
	//nolint:gosec // Binary and arguments come from the closed candidate registry.
	command := exec.CommandContext(context.Background(), binary, item.arguments...)
	command.Env = append(
		os.Environ(),
		"BENCH_BUSINESS_ADDRESS="+businessAddress,
		"BENCH_MANAGEMENT_ADDRESS="+managementAddress,
	)
	var diagnostic bytes.Buffer
	command.Stdout = io.Discard
	command.Stderr = &diagnostic
	started := time.Now()
	if err := command.Start(); err != nil {
		return measure.Sample{}, err
	}
	running := true
	defer func() {
		if running {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	if err := waitForProbe("http://" + managementAddress + "/startupz"); err != nil {
		return measure.Sample{}, fmt.Errorf("%s startup: %w", item.name, err)
	}
	startupMilliseconds := milliseconds(time.Since(started))
	time.Sleep(25 * time.Millisecond)
	rssBytes, err := residentBytes(command.Process.Pid)
	if err != nil {
		return measure.Sample{}, err
	}
	prefix := fmt.Sprintf("%s-%s-%02d", item.command, state, sampleNumber)
	postalLoad, postalRaw, err := measureWorkload(
		rawDirectory,
		prefix+"-postal-json-rpc.json",
		settings.requests,
		settings.concurrency,
		http.MethodPost,
		"http://"+businessAddress+"/postal/search",
		`{"jsonrpc":"2.0","method":"postal.search","params":{"query":"00100"}}`,
	)
	if err != nil {
		return measure.Sample{}, fmt.Errorf("%s Postal JSON-RPC: %w", item.name, err)
	}
	trackIngestionLoad, trackIngestionRaw, err := measureWorkload(
		rawDirectory,
		prefix+"-track-ingestion.json",
		settings.requests,
		settings.concurrency,
		http.MethodPost,
		"http://"+businessAddress+"/track/ingest",
		`{"tracking_number":"JJFI000001","events":["picked-up","in-transit"]}`,
	)
	if err != nil {
		return measure.Sample{}, fmt.Errorf("%s Track ingestion: %w", item.name, err)
	}
	trackJSONRPCLoad, trackJSONRPCRaw, err := measureWorkload(
		rawDirectory,
		prefix+"-track-json-rpc.json",
		settings.requests,
		settings.concurrency,
		http.MethodPost,
		"http://"+businessAddress+"/track/rpc",
		`{"jsonrpc":"2.0","method":"track.ingest","params":{"tracking_number":"JJFI000001","events":["picked-up","in-transit"]}}`,
	)
	if err != nil {
		return measure.Sample{}, fmt.Errorf("%s Track JSON-RPC: %w", item.name, err)
	}
	locationLoad, locationRaw, err := measureWorkload(
		rawDirectory,
		prefix+"-location-lookup.json",
		settings.requests,
		settings.concurrency,
		http.MethodPost,
		"http://"+businessAddress+"/location/lookup",
		`{"carrier":"posti","codes":["001","002","003"]}`,
	)
	if err != nil {
		return measure.Sample{}, fmt.Errorf("%s Location lookup: %w", item.name, err)
	}
	probeLoad, probeRaw, err := measureWorkload(
		rawDirectory,
		prefix+"-probe.json",
		settings.probeRequests,
		settings.concurrency,
		http.MethodGet,
		"http://"+managementAddress+"/readyz",
		"",
	)
	if err != nil {
		return measure.Sample{}, fmt.Errorf("%s probe: %w", item.name, err)
	}
	shutdownStarted := time.Now()
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		return measure.Sample{}, err
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case waitErr := <-wait:
		if err := expectedSignalExit(waitErr); err != nil {
			return measure.Sample{}, fmt.Errorf(
				"%s exit: %w: %s",
				item.name,
				err,
				strings.TrimSpace(diagnostic.String()),
			)
		}
	case <-time.After(processTimeout):
		return measure.Sample{}, fmt.Errorf("%s shutdown exceeded %s", item.name, processTimeout)
	}
	shutdownMilliseconds := milliseconds(time.Since(shutdownStarted))
	running = false
	configuredDrainMilliseconds := 0.0
	if !item.incompatible {
		configuredDrainMilliseconds, err = measureConfiguredDrain(item, binary)
		if err != nil {
			return measure.Sample{}, fmt.Errorf(
				"%s configured drain: %w",
				item.name,
				err,
			)
		}
	}

	return measure.Sample{
		StartupMilliseconds:         startupMilliseconds,
		IdleRSSBytes:                rssBytes,
		JSONRPC:                     postalLoad,
		TrackIngestion:              trackIngestionLoad,
		TrackJSONRPC:                trackJSONRPCLoad,
		LocationLookup:              locationLoad,
		Probe:                       probeLoad,
		ShutdownMilliseconds:        shutdownMilliseconds,
		ConfiguredDrainMilliseconds: configuredDrainMilliseconds,
		ConfiguredDrainSupported:    !item.incompatible,
		JSONRPCRaw:                  postalRaw,
		TrackIngestionRaw:           trackIngestionRaw,
		TrackJSONRPCRaw:             trackJSONRPCRaw,
		LocationLookupRaw:           locationRaw,
		ProbeRaw:                    probeRaw,
	}, nil
}

func measureConfiguredDrain(item candidate, binary string) (float64, error) {
	businessAddress, err := availableAddress()
	if err != nil {
		return 0, err
	}
	managementAddress, err := availableAddress()
	if err != nil {
		return 0, err
	}
	//nolint:gosec // Binary and arguments come from the closed candidate registry.
	command := exec.CommandContext(context.Background(), binary, item.arguments...)
	command.Env = append(
		os.Environ(),
		"BENCH_BUSINESS_ADDRESS="+businessAddress,
		"BENCH_MANAGEMENT_ADDRESS="+managementAddress,
	)
	var diagnostic bytes.Buffer
	command.Stdout = io.Discard
	command.Stderr = &diagnostic
	if err := command.Start(); err != nil {
		return 0, err
	}
	running := true
	defer func() {
		if running {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	if err := waitForProbe("http://" + managementAddress + "/startupz"); err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"http://"+businessAddress+"/_benchmark/drain",
		strings.NewReader(`{}`),
	)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Timeout: processTimeout}).Do(request)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("drain response status %d", response.StatusCode)
	}
	started := time.Now()
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		return 0, err
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case waitErr := <-wait:
		if err := expectedSignalExit(waitErr); err != nil {
			return 0, fmt.Errorf(
				"exit: %w: %s",
				err,
				strings.TrimSpace(diagnostic.String()),
			)
		}
	case <-time.After(processTimeout):
		return 0, fmt.Errorf("shutdown exceeded %s", processTimeout)
	}
	running = false

	return milliseconds(time.Since(started)), nil
}

func measureWorkload(
	rawDirectory string,
	rawName string,
	requests int,
	concurrency int,
	method string,
	url string,
	body string,
) (measure.Load, string, error) {
	document, err := runOHA(requests, concurrency, method, url, body)
	if err != nil {
		return measure.Load{}, "", err
	}
	path, digest, err := writeRaw(rawDirectory, rawName, document)
	if err != nil {
		return measure.Load{}, "", err
	}
	load, err := measure.ParseOHA(document)
	if err != nil {
		return measure.Load{}, "", err
	}

	return load, path + "#sha256=" + digest, nil
}

func runOHA(
	requests int,
	concurrency int,
	method string,
	url string,
	body string,
) ([]byte, error) {
	arguments := []string{
		"--no-tui",
		"--output-format", "json",
		"-n", strconv.Itoa(requests),
		"-c", strconv.Itoa(concurrency),
		"-t", benchmarkTimeout.String(),
		"-m", method,
		"--disable-compression",
	}
	if body != "" {
		arguments = append(
			arguments,
			"-T", "application/json",
			"-d", body,
		)
	}
	arguments = append(arguments, url)

	ctx, cancel := context.WithTimeout(context.Background(), benchmarkTimeout*2)
	defer cancel()
	//nolint:gosec // Arguments are bounded numeric settings and benchmark URLs.
	return exec.CommandContext(ctx, "oha", arguments...).Output()
}

func waitForProbe(url string) error {
	client := &http.Client{Timeout: 50 * time.Millisecond}
	deadline := time.Now().Add(processTimeout)
	for time.Now().Before(deadline) {
		request, err := http.NewRequestWithContext(
			context.Background(),
			http.MethodGet,
			url,
			nil,
		)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_, readErr := io.Copy(io.Discard, response.Body)
			closeErr := response.Body.Close()
			if readErr == nil && closeErr == nil && response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(time.Millisecond)
	}

	return errors.New("startup probe did not become successful")
}

func residentBytes(pid int) (int64, error) {
	//nolint:gosec // PID is the integer identifier returned by the started process.
	output, err := exec.CommandContext(
		context.Background(),
		"ps",
		"-o", "rss=",
		"-p", strconv.Itoa(pid),
	).Output()
	if err != nil {
		return 0, err
	}
	kibibytes, err := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	if err != nil {
		return 0, err
	}

	return kibibytes * 1024, nil
}

func buildCandidate(item candidate, destination string, state string) error {
	arguments := []string{
		"build",
		"-trimpath",
		"-ldflags=-s -w",
		"-o", destination,
	}
	switch state {
	case "disabled":
		arguments = append(arguments, "-tags=benchmark_disabled")
	case "logging":
		arguments = append(arguments, "-tags=benchmark_logging")
	case "tracing":
		arguments = append(arguments, "-tags=benchmark_tracing")
	}
	arguments = append(arguments, "./cmd/"+item.command)
	//nolint:gosec // Command path and destination are bounded by the output directory.
	command := exec.CommandContext(
		context.Background(),
		"go",
		arguments...,
	)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("build %s: %w: %s", item.name, err, strings.TrimSpace(string(output)))
	}

	return nil
}

func captureEnvironment() (environment, error) {
	root, err := commandOutput("git", "rev-parse", "--show-toplevel")
	if err != nil {
		return environment{}, err
	}
	revision, err := commandOutput("git", "rev-parse", "HEAD")
	if err != nil {
		return environment{}, err
	}
	digest, err := commandOutput(
		filepath.Join(root, "scripts", "gate-input-digest.sh"),
		"benchmark",
		"pkg/service/benchmarks/platform",
	)
	if err != nil {
		return environment{}, err
	}
	ohaVersion, err := commandOutput("oha", "--version")
	if err != nil {
		return environment{}, err
	}
	kernel, err := commandOutput("uname", "-a")
	if err != nil {
		return environment{}, err
	}

	return environment{
		OS:               runtime.GOOS,
		Architecture:     runtime.GOARCH,
		LogicalCPUs:      runtime.NumCPU(),
		GoVersion:        runtime.Version(),
		OHAVersion:       ohaVersion,
		Kernel:           kernel,
		SourceRevision:   revision,
		GateInputDigest:  digest,
		ExecutionStarted: time.Now().UTC().Format(time.RFC3339Nano),
	}, nil
}

func commandOutput(name string, arguments ...string) (string, error) {
	//nolint:gosec // Callers use fixed local tool names and bounded arguments.
	output, err := exec.CommandContext(
		context.Background(),
		name,
		arguments...,
	).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}

	return strings.TrimSpace(string(output)), nil
}

func availableAddress() (string, error) {
	listener, err := (&net.ListenConfig{}).Listen(
		context.Background(),
		"tcp",
		"127.0.0.1:0",
	)
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", err
	}

	return address, nil
}

func writeRaw(directory string, name string, document []byte) (string, string, error) {
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, document, 0o600); err != nil {
		return "", "", err
	}
	digest := sha256.Sum256(document)

	return path, hex.EncodeToString(digest[:]), nil
}

func writeReport(directory string, current report) error {
	document, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(directory, "report.json")
	temporary, err := os.CreateTemp(directory, ".report-*.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(document); err != nil {
		_ = temporary.Close()

		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()

		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}

	return os.Rename(temporaryPath, path)
}

func fileDigest(path string) (string, error) {
	//nolint:gosec // Path is a benchmark binary built in the selected artifact directory.
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func expectedSignalExit(waitErr error) error {
	if waitErr == nil {
		return errors.New("process exited successfully instead of reporting SIGTERM")
	}
	var exitError *exec.ExitError
	if !errors.As(waitErr, &exitError) {
		return waitErr
	}
	if exitError.ExitCode() != 143 {
		return waitErr
	}

	return nil
}

func assess(results []candidateResult) budgetResult {
	byCandidate := make(map[string]candidateResult)
	for _, result := range results {
		if result.State == "disabled" {
			byCandidate[result.Candidate] = result
		}
	}
	low, lowOK := byCandidate["low-level-service"]
	cohesive, cohesiveOK := byCandidate["cohesive-service"]
	if !lowOK || !cohesiveOK {
		return budgetResult{
			Passed:   false,
			Failures: []string{"disabled low-level and cohesive results are required"},
		}
	}
	var failures []string
	for _, result := range []candidateResult{low, cohesive} {
		checkLoad(
			&failures,
			result.Candidate+" Postal JSON-RPC",
			result.Summary.JSONRPC,
			500,
			1000,
			70_000,
		)
		checkLoad(
			&failures,
			result.Candidate+" Track ingestion",
			result.Summary.TrackIngestion,
			400,
			800,
			85_000,
		)
		checkLoad(
			&failures,
			result.Candidate+" Track JSON-RPC",
			result.Summary.TrackJSONRPC,
			500,
			1000,
			70_000,
		)
		checkLoad(
			&failures,
			result.Candidate+" Location lookup",
			result.Summary.LocationLookup,
			400,
			800,
			85_000,
		)
		check(&failures, result.Summary.Probe.SuccessRate == 1, result.Candidate+" probe success")
		check(&failures, result.Summary.StartupP95Milliseconds <= 75, result.Candidate+" startup p95")
		check(&failures, result.Summary.MaximumIdleRSSBytes <= 13*1024*1024, result.Candidate+" idle RSS")
		check(&failures, result.BinaryBytes <= 6*1024*1024, result.Candidate+" binary size")
		check(&failures, result.Summary.Probe.P95Microseconds <= 350, result.Candidate+" probe p95")
		check(&failures, result.Summary.ShutdownP95Milliseconds <= 20, result.Candidate+" shutdown p95")
		check(
			&failures,
			result.Summary.ConfiguredDrainSupported,
			result.Candidate+" configured drain support",
		)
		check(
			&failures,
			result.Summary.ConfiguredDrainP95Milliseconds <= milliseconds(
				workload.ShutdownTimeout+100*time.Millisecond,
			),
			result.Candidate+" configured drain p95",
		)
	}
	checkRelativeLoad(
		&failures,
		"Postal JSON-RPC",
		low.Summary.JSONRPC,
		cohesive.Summary.JSONRPC,
	)
	checkRelativeLoad(
		&failures,
		"Track ingestion",
		low.Summary.TrackIngestion,
		cohesive.Summary.TrackIngestion,
	)
	checkRelativeLoad(
		&failures,
		"Track JSON-RPC",
		low.Summary.TrackJSONRPC,
		cohesive.Summary.TrackJSONRPC,
	)
	checkRelativeLoad(
		&failures,
		"Location lookup",
		low.Summary.LocationLookup,
		cohesive.Summary.LocationLookup,
	)
	check(
		&failures,
		cohesive.Summary.StartupP95Milliseconds <= low.Summary.StartupP95Milliseconds*1.05,
		"cohesive relative startup",
	)
	check(
		&failures,
		cohesive.Summary.MaximumIdleRSSBytes <= low.Summary.MaximumIdleRSSBytes+512*1024,
		"cohesive relative idle RSS",
	)
	check(
		&failures,
		cohesive.BinaryBytes <= low.BinaryBytes+256*1024,
		"cohesive relative binary size",
	)
	check(
		&failures,
		cohesive.Summary.ShutdownP95Milliseconds <= low.Summary.ShutdownP95Milliseconds*1.05,
		"cohesive relative shutdown",
	)

	return budgetResult{Passed: len(failures) == 0, Failures: failures}
}

func checkLoad(
	failures *[]string,
	name string,
	load measure.Load,
	maximumP95 float64,
	maximumP99 float64,
	minimumThroughput float64,
) {
	check(failures, load.SuccessRate == 1, name+" success")
	check(failures, load.P95Microseconds <= maximumP95, name+" p95")
	check(failures, load.P99Microseconds <= maximumP99, name+" p99")
	check(
		failures,
		load.RequestsPerSecond >= minimumThroughput,
		name+" throughput",
	)
}

func checkRelativeLoad(
	failures *[]string,
	name string,
	low measure.Load,
	cohesive measure.Load,
) {
	check(
		failures,
		cohesive.P50Microseconds <= low.P50Microseconds*1.03,
		"cohesive relative "+name+" p50",
	)
	check(
		failures,
		cohesive.P95Microseconds <= low.P95Microseconds*1.03,
		"cohesive relative "+name+" p95",
	)
	check(
		failures,
		cohesive.RequestsPerSecond >= low.RequestsPerSecond*0.97,
		"cohesive relative "+name+" throughput",
	)
}

func check(failures *[]string, passed bool, name string) {
	if !passed {
		*failures = append(*failures, name)
	}
}

func selectCandidates(names []string) ([]candidate, error) {
	known := map[string]candidate{
		"plain-net-http": {
			name: "plain-net-http", command: "plain",
		},
		"low-level-service": {
			name: "low-level-service", command: "lowlevel",
		},
		"cohesive-service": {
			name: "cohesive-service", command: "cohesive", arguments: []string{"serve"},
		},
		"chi": {
			name: "chi", command: "chi",
		},
		"gin": {
			name: "gin", command: "gin",
		},
		"echo": {
			name: "echo", command: "echo",
		},
		"fiber-fasthttp": {
			name: "fiber-fasthttp", command: "fiber", incompatible: true,
		},
	}
	selected := make([]candidate, 0, len(names))
	for _, name := range names {
		item, exists := known[name]
		if !exists {
			return nil, fmt.Errorf("unknown candidate %q", name)
		}
		selected = append(selected, item)
	}

	return selected, nil
}

func split(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
