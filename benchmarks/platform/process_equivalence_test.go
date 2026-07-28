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
			build := exec.Command(
				"go",
				"build",
				"-trimpath",
				"-tags=benchmark_disabled",
				"-o",
				binary,
				"./cmd/"+candidate,
			)
			build.Stderr = io.Discard
			if err := build.Run(); err != nil {
				t.Fatalf("build %s: %v", candidate, err)
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
	const maximumOverhead = 256 * 1024
	if overhead := cohesiveInfo.Size() - lowLevelInfo.Size(); overhead > maximumOverhead {
		t.Fatalf(
			"cohesive binary overhead = %d bytes, budget = %d bytes",
			overhead,
			maximumOverhead,
		)
	}
}

func buildBinary(t *testing.T, candidate string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), candidate)
	command := exec.CommandContext(
		t.Context(),
		"go",
		"build",
		"-trimpath",
		"-tags=benchmark_disabled",
		"-ldflags=-s -w",
		"-o",
		binary,
		"./cmd/"+candidate,
	)
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		t.Fatalf("build %s: %v", candidate, err)
	}

	return binary
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
