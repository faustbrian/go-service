package platform_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessCandidatesPreserveEquivalentRuntimeBehavior(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process signal contract requires POSIX signals")
	}
	candidates := []string{
		"plain",
		"lowlevel",
		"cohesive",
		"chi",
		"gin",
		"echo",
		"fiber",
	}
	for _, candidate := range candidates {
		t.Run(candidate, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), candidate)
			build := candidateBuildCommand(t, binary, candidate, false)
			output, err := build.CombinedOutput()
			if err != nil {
				t.Fatalf("build %s: %v\n%s", candidate, err, output)
			}
			businessAddress := availableAddress(t)
			managementAddress := availableAddress(t)
			var arguments []string
			if candidate == "cohesive" {
				arguments = []string{"serve"}
			}
			command := exec.Command(binary, arguments...)
			command.Env = append(
				os.Environ(),
				"BENCH_BUSINESS_ADDRESS="+businessAddress,
				"BENCH_MANAGEMENT_ADDRESS="+managementAddress,
			)
			command.Stdout = io.Discard
			command.Stderr = io.Discard
			if err := command.Start(); err != nil {
				t.Fatalf("start %s: %v", candidate, err)
			}
			stopped := false
			t.Cleanup(func() {
				if !stopped && command.Process != nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			})

			startup := eventuallyRequest(
				t,
				http.MethodGet,
				"http://"+managementAddress+"/startupz",
				"",
			)
			assertProcessResponse(t, startup, http.StatusOK, `"probe":"startup"`)
			if startup.header.Get(correlationHeader) == "" ||
				startup.header.Get(requestHeader) == "" {
				t.Fatalf("startup identity headers = %v", startup.header)
			}
			search := eventuallyRequest(
				t,
				http.MethodPost,
				"http://"+businessAddress+"/postal/search",
				postalSearchBody,
			)
			assertProcessResponse(
				t,
				search,
				http.StatusOK,
				`{"jsonrpc":"2.0","result":["00100","00101","00102"]}`,
			)
			if search.header.Get(correlationHeader) == "" ||
				search.header.Get(requestHeader) == "" {
				t.Fatalf("search identity headers = %v", search.header)
			}
			if got := search.header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Fatalf("search content-type options = %q, want nosniff", got)
			}
			workloads := []struct {
				path     string
				body     string
				expected string
			}{
				{
					path:     "/track/ingest",
					body:     trackIngestBody,
					expected: `{"accepted":2,"child_hops":2}`,
				},
				{
					path:     "/track/rpc",
					body:     trackRPCBody,
					expected: `{"jsonrpc":"2.0","result":{"accepted":2,"child_hops":2}}`,
				},
				{
					path:     "/location/lookup",
					body:     locationLookupBody,
					expected: `{"locations":[{"code":"001"},{"code":"002"},{"code":"003"}]}`,
				},
			}
			for _, workload := range workloads {
				actual := eventuallyRequest(
					t,
					http.MethodPost,
					"http://"+businessAddress+workload.path,
					workload.body,
				)
				assertProcessResponse(
					t,
					actual,
					http.StatusOK,
					workload.expected,
				)
				if actual.header.Get(correlationHeader) == "" ||
					actual.header.Get(requestHeader) == "" {
					t.Fatalf(
						"%s identity headers = %v",
						workload.path,
						actual.header,
					)
				}
			}

			shutdownStarted := time.Now()
			if err := command.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatalf("signal %s: %v", candidate, err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			select {
			case err := <-done:
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) || exitError.ExitCode() != 143 {
					t.Fatalf("wait %s: %v", candidate, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("%s did not stop", candidate)
			}
			stopped = true
			if elapsed := time.Since(shutdownStarted); elapsed > time.Second {
				t.Fatalf("%s shutdown = %s", candidate, elapsed)
			}
		})
	}
}

func TestCohesiveBinaryOverheadStaysWithinFrozenBudget(t *testing.T) {
	t.Parallel()

	lowLevel := buildBinary(t, "lowlevel")
	cohesive := buildBinary(t, "cohesive")
	lowLevelInfo, err := os.Stat(lowLevel)
	if err != nil {
		t.Fatalf("stat low-level binary: %v", err)
	}
	cohesiveInfo, err := os.Stat(cohesive)
	if err != nil {
		t.Fatalf("stat cohesive binary: %v", err)
	}
	const maximumOverhead = 384 * 1024
	if overhead := cohesiveInfo.Size() - lowLevelInfo.Size(); overhead > maximumOverhead {
		t.Fatalf(
			"cohesive binary overhead = %d bytes, budget = %d bytes",
			overhead,
			maximumOverhead,
		)
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" ||
		runtime.NumCPU() != 16 || runtime.GOMAXPROCS(0) != runtime.NumCPU() ||
		os.Getenv("GOGC") != "" || os.Getenv("GOMEMLIMIT") != "" ||
		os.Getenv("GODEBUG") != "" || runtime.Version() != "go1.26.6" {
		return
	}
	const maximumCohesiveBytes = 25 * 1024 * 1024 / 4
	if cohesiveInfo.Size() > maximumCohesiveBytes {
		t.Fatalf(
			"cohesive binary = %d bytes, budget = %d bytes",
			cohesiveInfo.Size(),
			maximumCohesiveBytes,
		)
	}
}

func buildBinary(t *testing.T, candidate string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), candidate)
	command := candidateBuildCommand(t, binary, candidate, true)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s: %v\n%s", candidate, err, output)
	}

	return binary
}

func candidateBuildCommand(
	t *testing.T,
	binary string,
	candidate string,
	strip bool,
) *exec.Cmd {
	t.Helper()
	arguments := []string{
		"build",
		"-trimpath",
		"-tags=benchmark_disabled",
	}
	if strip {
		arguments = append(arguments, "-ldflags=-s -w")
	}
	arguments = append(arguments, "-o", binary, "./cmd/"+candidate)

	command := exec.CommandContext(t.Context(), "go", arguments...)
	if os.Getenv("GOLIB_LOCAL_PROXY") == "" {
		return command
	}

	modfile := filepath.Join(t.TempDir(), "candidate.mod")
	module, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("read benchmark module: %v", err)
	}
	if err = os.WriteFile(modfile, module, 0o600); err != nil {
		t.Fatalf("write candidate module: %v", err)
	}
	checksum, err := os.ReadFile("go.sum")
	if err != nil {
		t.Fatalf("read benchmark checksums: %v", err)
	}
	lines := strings.Split(string(checksum), "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if !strings.HasPrefix(line, "github.com/faustbrian/golib/") {
			filtered = append(filtered, line)
		}
	}
	if err = os.WriteFile(
		strings.TrimSuffix(modfile, ".mod")+".sum",
		[]byte(strings.Join(filtered, "\n")),
		0o600,
	); err != nil {
		t.Fatalf("write candidate checksums: %v", err)
	}
	arguments = append([]string{"build", "-modfile=" + modfile, "-mod=mod"}, arguments[1:]...)
	goExecutable := os.Getenv("GOLIB_REAL_GO")
	if goExecutable == "" {
		t.Fatal("local proxy candidate build requires GOLIB_REAL_GO")
	}
	command = exec.CommandContext(t.Context(), goExecutable, arguments...)
	environment := make([]string, 0, len(os.Environ())+2)
	for _, variable := range os.Environ() {
		if strings.HasPrefix(variable, "GOFLAGS=") || strings.HasPrefix(variable, "GOWORK=") {
			continue
		}
		environment = append(environment, variable)
	}
	command.Env = append(
		environment,
		"GOWORK=off",
		"GOFLAGS="+os.Getenv("GOLIB_UPSTREAM_GOFLAGS"),
	)

	return command
}

func availableAddress(t *testing.T) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(
		context.Background(),
		"tcp",
		"127.0.0.1:0",
	)
	if err != nil {
		t.Fatalf("reserve address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release address: %v", err)
	}

	return address
}

func eventuallyRequest(
	t *testing.T,
	method string,
	url string,
	body string,
) response {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		request, err := http.NewRequest(method, url, bytes.NewBufferString(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		httpResponse, err := http.DefaultClient.Do(request)
		if err == nil {
			bodyBytes, readErr := io.ReadAll(httpResponse.Body)
			closeErr := httpResponse.Body.Close()
			if readErr == nil && closeErr == nil {
				return response{
					statusCode: httpResponse.StatusCode,
					header:     httpResponse.Header,
					body:       string(bodyBytes),
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s %s did not respond", method, url)

	return response{}
}

func assertProcessResponse(
	t *testing.T,
	actual response,
	status int,
	body string,
) {
	t.Helper()
	if actual.statusCode != status ||
		!strings.Contains(strings.TrimSpace(actual.body), body) {
		t.Fatalf("response = %d %q", actual.statusCode, actual.body)
	}
}
