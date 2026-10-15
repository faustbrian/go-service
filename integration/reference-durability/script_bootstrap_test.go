package referencedurability_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const stopAfterImageExpansion = 97
const stopAtGoInvocation = 98

func TestDurabilityScriptsResolveRepositoryAndPinnedImagesBeforeDocker(t *testing.T) {
	t.Parallel()

	moduleDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	postgresImages := readPostgresImages(t, filepath.Join(
		moduleDirectory,
		"testdata",
		"postgres-images.tsv",
	))
	valkeyImage := readImage(t, filepath.Join(
		moduleDirectory,
		"testdata",
		"valkey-image.txt",
	), "9.1.0")

	for _, test := range []struct {
		name            string
		script          string
		postgresVersion string
	}{
		{name: "durability", script: "check-durability.sh", postgresVersion: "18"},
		{name: "recovery", script: "check-recovery.sh", postgresVersion: "18"},
		{name: "version matrix", script: "check-version-matrix.sh", postgresVersion: "14"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			log := runUntilDockerImagesExpand(t, filepath.Join(moduleDirectory, test.script))
			for _, image := range []string{postgresImages[test.postgresVersion], valkeyImage} {
				if !strings.Contains(log, image) {
					t.Fatalf("Docker calls do not contain pinned image %q:\n%s", image, log)
				}
			}
		})
	}
}

func TestOperationalScriptsBootstrapStandaloneModules(t *testing.T) {
	t.Parallel()

	moduleDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	for _, test := range []struct {
		script              string
		arguments           string
		platformLoad        bool
		buildkitPreexisting bool
	}{
		{script: "check-durability.sh", arguments: "test -tags=integration -race -v ."},
		{script: "check-recovery.sh", arguments: "build -trimpath -o"},
		{script: "check-version-matrix.sh", arguments: "test -tags=integration ."},
		{script: "../reference-platform/check-platform.sh", arguments: "build -trimpath -o"},
		{script: "../reference-platform/check-platform.sh", arguments: "build -trimpath -o", buildkitPreexisting: true},
		{script: "../reference-platform/check-load.sh", arguments: "build -trimpath -o"},
		{script: "../reference-platform/check-load.sh", arguments: "run ./cmd/platform-load", platformLoad: true},
	} {
		t.Run(test.script+test.arguments+map[bool]string{true: "_shared-buildkit", false: ""}[test.buildkitPreexisting], func(t *testing.T) {
			t.Parallel()
			script := filepath.Join(moduleDirectory, test.script)
			invocation, dockerLog := runUntilGoInvocation(t, script, test.platformLoad, test.buildkitPreexisting)
			if invocation["PWD"] != filepath.Dir(script) {
				t.Errorf("Go working directory = %q, want nested module %q", invocation["PWD"], filepath.Dir(script))
			}
			if !strings.HasPrefix(invocation["ARGS"], test.arguments+" ") {
				t.Errorf("Go arguments = %q, want prefix %q", invocation["ARGS"], test.arguments)
			}
			for key, want := range map[string]string{
				"GOENV": "off", "GOWORK": "off", "GOTOOLCHAIN": "local",
				"GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org",
				"GOPRIVATE": "", "GONOPROXY": "", "GONOSUMDB": "",
				"GOFLAGS": "-mod=readonly",
			} {
				if invocation[key] != want {
					t.Errorf("Go %s = %q, want %q", key, invocation[key], want)
				}
			}
			cacheRoot := filepath.Dir(invocation["GOCACHE"])
			for _, key := range []string{"GOCACHE", "GOMODCACHE", "GOTMPDIR"} {
				path := invocation[key]
				if !filepath.IsAbs(path) || filepath.Dir(path) != cacheRoot || invocation[key+"_EXISTS"] != "yes" {
					t.Errorf("%s = %q must be an existing private sibling cache at invocation", key, path)
				}
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s = %q not cleaned after controlled failure: %v", key, path, err)
				}
			}
			if strings.Contains(test.script, "reference-platform") {
				repository := filepath.Dir(filepath.Dir(filepath.Dir(script)))
				buildkitImage := "moby/buildkit@sha256:cec9f139f45e93c5c69c60f8b07cfad9f43f4ef6b6a6cd917527fea5ff2e3dea"
				if !strings.Contains(dockerLog, "--driver-opt image="+buildkitImage+"\n") {
					t.Errorf("Docker builder did not select its pinned external input: %s", dockerLog)
				}
				removedBuildkit := strings.Contains(dockerLog, "call=image rm "+buildkitImage+"\n")
				if removedBuildkit == test.buildkitPreexisting {
					t.Errorf("BuildKit removal = %t for preexisting = %t", removedBuildkit, test.buildkitPreexisting)
				}
				if !strings.Contains(dockerLog, "build-context="+repository+"\n") {
					t.Errorf("Docker build context must be standalone repository %q:\n%s", repository, dockerLog)
				}
			}
		})
	}
}

func TestPlatformScriptValidatesNativeSelectionBeforeResources(t *testing.T) {
	t.Parallel()
	moduleDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(moduleDirectory, "../reference-platform/check-platform.sh")
	for _, test := range []struct {
		arguments []string
		host      string
		accepted  bool
	}{
		{arguments: []string{"invalid"}, host: "x86_64"},
		{arguments: []string{"arm64"}, host: "x86_64"},
		{arguments: []string{"amd64", "arm64"}, host: "x86_64"},
		{arguments: []string{"amd64"}, host: "x86_64", accepted: true},
		{arguments: []string{"arm64"}, host: "aarch64", accepted: true},
		{host: "x86_64", accepted: true},
	} {
		t.Run(strings.Join(test.arguments, "_")+test.host, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			boundaryLog := filepath.Join(directory, "boundary.log")
			for _, name := range []string{"docker", "go", "mktemp"} {
				if err := os.WriteFile(filepath.Join(directory, name), []byte("#!/bin/sh\ntouch \"$BOUNDARY_LOG\"\nexit 99\n"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(directory, "uname"), []byte("#!/bin/sh\nprintf '%s\\n' \"$TEST_HOST_ARCHITECTURE\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/bin/sh", append([]string{script}, test.arguments...)...)
			command.Dir = directory
			command.Env = append(os.Environ(), "PATH="+directory+":"+os.Getenv("PATH"), "BOUNDARY_LOG="+boundaryLog, "TEST_HOST_ARCHITECTURE="+test.host)
			output, err := command.CombinedOutput()
			wantExit := 2
			if test.accepted {
				wantExit = 99
			}
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) || exitError.ExitCode() != wantExit {
				t.Fatalf("arguments %v on %s exit = %v, output = %s, want %d", test.arguments, test.host, err, output, wantExit)
			}
			_, err = os.Stat(boundaryLog)
			if test.accepted && err != nil {
				t.Fatalf("accepted selection did not reach resource boundary: %v", err)
			}
			if !test.accepted && !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refused selection crossed resource boundary: %v", err)
			}
		})
	}
}

func TestPlatformScriptsDoNotRemoveUncreatedDockerResources(t *testing.T) {
	t.Parallel()
	moduleDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"check-platform.sh", "check-load.sh"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			directory := t.TempDir()
			dockerLog := filepath.Join(directory, "docker.log")
			for name, source := range map[string]string{
				"go":     "#!/bin/sh\nexit 98\n",
				"docker": "#!/bin/sh\nprintf '%s\\n' \"$*\" >>\"$DOCKER_LOG\"\n",
			} {
				if err := os.WriteFile(filepath.Join(directory, name), []byte(source), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/bin/sh", filepath.Join(moduleDirectory, "../reference-platform", name))
			command.Dir = directory
			command.Env = append(os.Environ(), "PATH="+directory+":"+os.Getenv("PATH"), "DOCKER_LOG="+dockerLog, "TMPDIR="+directory)
			output, err := command.CombinedOutput()
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) || exitError.ExitCode() != stopAtGoInvocation {
				t.Fatalf("fixture build failure exit = %v, output = %s", err, output)
			}
			if _, err := os.Stat(dockerLog); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("early build failure invoked Docker cleanup: %v", err)
			}
		})
	}
}

func TestPlatformScriptsCleanCreatedContainerWhenStartFails(t *testing.T) {
	t.Parallel()
	moduleDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"check-platform.sh", "check-load.sh"} {
		for _, failure := range []string{"create", "start"} {
			t.Run(name+"_"+failure, func(t *testing.T) {
				t.Parallel()
				_, dockerLog := runUntilGoInvocation(t, filepath.Join(moduleDirectory, "../reference-platform", name), true, false, failure)
				if failure == "start" && !strings.Contains(dockerLog, "call=start task-container-id\n") {
					t.Fatalf("created container was not started by its owned ID: %s", dockerLog)
				}
			})
		}
	}
}

func runUntilGoInvocation(t *testing.T, script string, platformLoad bool, buildkitPreexisting bool, containerFailure ...string) (map[string]string, string) {
	t.Helper()

	directory := t.TempDir()
	binDirectory := filepath.Join(directory, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatalf("create fake binary directory: %v", err)
	}
	goLog := filepath.Join(directory, "go.log")
	dockerLog := filepath.Join(directory, "docker.log")
	failureMode := ""
	if len(containerFailure) != 0 {
		failureMode = containerFailure[0]
	}
	containerState := filepath.Join(directory, "created-container")
	preexistingContainer := filepath.Join(directory, "preexisting-container")
	if err := os.WriteFile(preexistingContainer, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	commands := map[string]string{
		"docker": `#!/bin/sh
printf 'call=%s\n' "$*" >>"$DOCKER_LOG"
if [ "$1 $2" = 'image inspect' ] && [ "${3:-}" != --format ]; then
	case "${3:-}:$BUILDKIT_PREEXISTING" in
		moby/buildkit@sha256:*:true) exit 0 ;;
		*) exit 1 ;;
	esac
fi
if [ "$1 $2" = 'image inspect' ] && [ "${3:-}" = --format ]; then
	case "$4" in
		*Architecture*) printf 'amd64\n' ;;
		*Config.User*) printf '65532:65532\n' ;;
	esac
fi
if [ "$1" = run ] || [ "$1" = create ]; then
	[ "$CONTAINER_FAILURE" != create ] || exit 125
	if [ "$CONTAINER_FAILURE" = start ]; then
		touch "$CONTAINER_STATE"
		[ "$1" != run ] || exit 125
	fi
	if [ "$1" = create ]; then printf 'task-container-id\n'; fi
fi
if [ "$1" = start ] && [ "$CONTAINER_FAILURE" = start ]; then exit 125; fi
if [ "$1" = rm ]; then
	for argument do last=$argument; done
	if [ "$last" = task-container-id ]; then
		rm -f "$CONTAINER_STATE"
	elif [ -n "$CONTAINER_FAILURE" ]; then
		rm -f "$PREEXISTING_CONTAINER"
	fi
fi
if [ "$1 $2" = 'buildx build' ]; then
	for argument do last=$argument; done
	printf 'build-context=%s\n' "$last" >>"$DOCKER_LOG"
	[ "$PLATFORM_LOAD" = true ] || exit 98
fi
if [ "$1" = port ]; then printf '127.0.0.1:54321\n'; fi
if [ "$1" = exec ]; then
	case "$*" in
		*server_version_num*) printf '140000\n' ;;
		*'INFO server'*) printf 'valkey_version:9.1.0\n' ;;
	esac
fi
exit 0
`,
		"go": `#!/bin/sh
if [ "$1 $2" = 'env GOVERSION' ]; then printf 'go1.27.2\n'; exit 0; fi
{
	printf 'PWD=%s\nARGS=%s\n' "$PWD" "$*"
	for key in GOENV GOWORK GOTOOLCHAIN GOPROXY GOSUMDB GOPRIVATE GONOPROXY GONOSUMDB GOFLAGS GOCACHE GOMODCACHE GOTMPDIR; do
		eval 'value=${'"$key"'-}'
		printf '%s=%s\n' "$key" "$value"
		if [ -d "$value" ]; then printf '%s_EXISTS=yes\n' "$key"; fi
	done
} >"$GO_LOG"
case "$*" in
	*'./cmd/platform-fixture')
		[ "$1" = build ] || exit 96
		while [ "$#" -gt 0 ]; do
			if [ "$1" = -o ]; then shift; output=$1; fi
			shift
		done
		cat >"$output" <<'FIXTURE'
#!/bin/sh
printf '%s\n' "$*" >"$FIXTURE_LOG"
while [ "$#" -gt 0 ]; do
	if [ "$1" = -ready ]; then shift; printf '54321\n' >"$1"; fi
	shift
done
FIXTURE
		chmod +x "$output"
		exit 0 ;;
esac
exit 98
`,
		"curl": "#!/bin/sh\nexit 0\n",
		"jq":   "#!/bin/sh\nexit 0\n",
	}
	for name, source := range commands {
		if err := os.WriteFile(filepath.Join(binDirectory, name), []byte(source), 0o700); err != nil {
			t.Fatalf("write fake %s command: %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", script)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+":"+os.Getenv("PATH"),
		"GO_LOG="+goLog, "DOCKER_LOG="+dockerLog, "FIXTURE_LOG="+filepath.Join(directory, "fixture.log"),
		"PLATFORM_LOAD="+map[bool]string{true: "true", false: "false"}[platformLoad],
		"BUILDKIT_PREEXISTING="+map[bool]string{true: "true", false: "false"}[buildkitPreexisting],
		"CONTAINER_FAILURE="+failureMode, "CONTAINER_STATE="+containerState, "PREEXISTING_CONTAINER="+preexistingContainer,
		"TMPDIR="+directory,
		"GOENV="+filepath.Join(directory, "untrusted-goenv"), "GOWORK="+filepath.Join(directory, "untrusted-go.work"),
		"GOTOOLCHAIN=auto", "GOPROXY=off", "GOSUMDB=off",
		"GOPRIVATE=*", "GONOPROXY=*", "GONOSUMDB=*", "GOFLAGS=-mod=mod",
		"GOCACHE="+filepath.Join(directory, "ambient-build"),
		"GOMODCACHE="+filepath.Join(directory, "ambient-mod"),
		"GOTMPDIR="+filepath.Join(directory, "ambient-tmp"),
	)
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	wantExit := stopAtGoInvocation
	if failureMode != "" {
		wantExit = 125
	}
	if !errors.As(err, &exitError) || exitError.ExitCode() != wantExit {
		t.Fatalf("%s exit = %v, output = %s, want controlled exit %d", filepath.Base(script), err, output, wantExit)
	}
	if _, err := os.Stat(containerState); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created container remains after start failure: %v", err)
	}
	if _, err := os.Stat(preexistingContainer); err != nil {
		t.Fatalf("cleanup removed preexisting container: %v", err)
	}
	log, err := os.ReadFile(goLog)
	if err != nil {
		t.Fatalf("read fake Go invocation: %v", err)
	}
	invocation := make(map[string]string)
	for line := range strings.SplitSeq(strings.TrimSpace(string(log)), "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			t.Fatalf("invalid invocation log line: %q", line)
		}
		invocation[key] = value
	}
	if strings.Contains(script, "reference-platform") {
		fixtureLog, err := os.ReadFile(filepath.Join(directory, "fixture.log"))
		if err != nil || !strings.Contains(string(fixtureLog), "-certificate ") || !strings.Contains(string(fixtureLog), "-ready ") {
			t.Fatalf("built fixture was not launched with certificate/readiness arguments: %s, %v", fixtureLog, err)
		}
	}
	dockerCalls, err := os.ReadFile(dockerLog)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read Docker calls: %v", err)
	}
	return invocation, string(dockerCalls)
}

func runUntilDockerImagesExpand(t *testing.T, script string) string {
	t.Helper()

	directory := t.TempDir()
	binDirectory := filepath.Join(directory, "bin")
	if err := os.Mkdir(binDirectory, 0o700); err != nil {
		t.Fatalf("create fake binary directory: %v", err)
	}
	dockerLog := filepath.Join(directory, "docker.log")
	dockerState := filepath.Join(directory, "docker.state")
	fakeDocker := filepath.Join(binDirectory, "docker")
	if err := os.WriteFile(fakeDocker, []byte(`#!/bin/sh
printf '%s\n' "$*" >>"$DOCKER_LOG"
if [ "$1" = port ]; then
	printf '127.0.0.1:54321\n'
	exit 0
fi
if [ "$1" = run ]; then
	count=0
	if [ -f "$DOCKER_STATE" ]; then
		read -r count <"$DOCKER_STATE"
	fi
	count=$((count + 1))
	printf '%s\n' "$count" >"$DOCKER_STATE"
	if [ "$count" -eq 2 ]; then
		exit 97
	fi
fi
exit 0
`), 0o700); err != nil {
		t.Fatalf("write fake Docker command: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/bin/sh", script)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"PATH="+binDirectory+":"+os.Getenv("PATH"),
		"DOCKER_LOG="+dockerLog,
		"DOCKER_STATE="+dockerState,
	)
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != stopAfterImageExpansion {
		t.Fatalf(
			"%s exit = %v, output = %s, want controlled exit %d",
			filepath.Base(script),
			err,
			output,
			stopAfterImageExpansion,
		)
	}
	log, err := os.ReadFile(dockerLog)
	if err != nil {
		t.Fatalf("read fake Docker calls: %v", err)
	}

	return string(log)
}

func readPostgresImages(t *testing.T, filename string) map[string]string {
	t.Helper()

	file, err := os.Open(filename)
	if err != nil {
		t.Fatalf("open PostgreSQL image matrix: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close PostgreSQL image matrix: %v", err)
		}
	}()

	images := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 {
			images[fields[0]] = fields[1]
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan PostgreSQL image matrix: %v", err)
	}

	return images
}

func readImage(t *testing.T, filename string, version string) string {
	t.Helper()

	content, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read image identity: %v", err)
	}
	fields := strings.Fields(string(content))
	if len(fields) != 2 || fields[0] != version {
		t.Fatalf("image identity = %q, want version %s and one image", content, version)
	}
	return fields[1]
}
