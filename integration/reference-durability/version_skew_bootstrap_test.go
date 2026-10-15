package referencedurability_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionSkewBuildsIndependentConsumersAndCleansPrivateCaches(t *testing.T) {
	t.Parallel()
	log, directory := runVersionSkewBoundary(t, "build-only", 0)
	for _, version := range []string{"v1.1.0", "v1.1.2"} {
		if !strings.Contains(log, "/"+version+"\n") || !strings.Contains(log, "identity -queue-version "+version) {
			t.Fatalf("independent public consumer %s was not built and checked: %s", version, log)
		}
	}
	for _, setting := range []string{"GOENV=off", "GOWORK=off", "GOTOOLCHAIN=local", "GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org", "GOPRIVATE=", "GONOPROXY=", "GONOSUMDB=", "GOFLAGS=-mod=readonly"} {
		if !strings.Contains(log, setting+"\n") {
			t.Fatalf("consumer did not isolate %s", setting)
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "golib-queue-skew.") {
			t.Fatal("consumer private run/caches survived cleanup")
		}
	}
}

func TestVersionSkewRefusesSourceOrBuiltVersionDriftBeforeDocker(t *testing.T) {
	t.Parallel()
	for mode, status := range map[string]int{"wrong-source": 1, "wrong-binary": 96, "preexisting-volume": 1} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			log, _ := runVersionSkewBoundary(t, mode, status)
			if strings.Contains(log, "docker=create") || strings.Contains(log, "docker=volume create") || strings.Contains(log, "docker=rm") || strings.Contains(log, "docker=volume rm") {
				t.Fatal("refused bootstrap mutated Docker resources")
			}
		})
	}
}

func TestVersionSkewCleansCreatedContainerAndOwnedVolumeAfterStartFailure(t *testing.T) {
	t.Parallel()
	log, directory := runVersionSkewBoundary(t, "start-failure", 125)
	if !strings.Contains(log, "docker=rm --force task-container-id\n") || !strings.Contains(log, "docker=volume rm ") {
		t.Fatalf("failed container start did not release created resources: %s", log)
	}
	if strings.Contains(log, "docker=image rm ") {
		t.Fatal("cleanup removed preexisting pinned backend image")
	}
	if _, err := os.Stat(filepath.Join(directory, "container")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created container survived failure")
	}
}

func runVersionSkewBoundary(t *testing.T, mode string, wantStatus int) (string, string) {
	t.Helper()
	module, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"go": `#!/bin/sh
printf 'cwd=%s\ngo=%s\n' "$PWD" "$*" >>"$BOUNDARY_LOG"
for key in GOENV GOWORK GOTOOLCHAIN GOPROXY GOSUMDB GOPRIVATE GONOPROXY GONOSUMDB GOFLAGS GOCACHE GOMODCACHE GOTMPDIR; do
 eval 'value=${'"$key"'-}'
 printf '%s=%s\n' "$key" "$value" >>"$BOUNDARY_LOG"
done
case "$1 $2" in
 'mod download')
  version=${4##*@}
  case "$version" in
   v1.1.0) source=29797a97f747555d0cd00aa38dfe11f22e08f456 ;;
   v1.1.2) source=2173052b630557f345d02ec6961594218d47a316 ;;
  esac
  [ "$BOUNDARY_MODE" != wrong-source ] || source=untrusted
  printf '{"Path":"github.com/faustbrian/go-queue","Version":"%s","Origin":{"Hash":"%s"},"Sum":"h1:fixture","GoModSum":"h1:fixture"}\n' "$version" "$source" ;;
 'build -trimpath')
  built=${PWD##*/}
  [ "$BOUNDARY_MODE" != wrong-binary ] || built=v1.1.2
  printf '#!/bin/sh\nbuilt=%s\n' "$built" >consumer
  cat >>consumer <<'CONSUMER'
printf 'consumer=%s\n' "$*" >>"$BOUNDARY_LOG"
[ "$1 $2 $3 $4" = "-mode identity -queue-version $built" ] || exit 96
printf '{"queue_version":"%s"}\n' "$built"
CONSUMER
  chmod +x consumer ;;
esac
`,
		"docker": `#!/bin/sh
printf 'docker=%s\n' "$*" >>"$BOUNDARY_LOG"
case "$1 $2" in
 'volume inspect')
  if [ "${3:-}" = --format ]; then cat "$BOUNDARY_DIRECTORY/owner"; exit 0; fi
  [ "$BOUNDARY_MODE" = preexisting-volume ] && exit 0
  exit 1 ;;
 'volume create')
  printf '%s\n' "${4#*=}" >"$BOUNDARY_DIRECTORY/owner" ;;
 'image inspect') exit 0 ;;
 'start task-container-id') exit 125 ;;
 'rm --force') rm -f "$BOUNDARY_DIRECTORY/container" ;;
esac
if [ "$1" = create ]; then touch "$BOUNDARY_DIRECTORY/container"; printf 'task-container-id\n'; fi
`,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(source), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	arguments := []string{filepath.Join(module, "check-version-skew.sh")}
	if mode == "build-only" || mode == "wrong-source" || mode == "wrong-binary" {
		arguments = append(arguments, "--build-only")
	}
	command := exec.CommandContext(ctx, "/bin/sh", arguments...)
	command.Dir = directory
	logPath := filepath.Join(directory, "boundary.log")
	command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "TMPDIR="+directory,
		"BOUNDARY_MODE="+mode, "BOUNDARY_LOG="+logPath, "BOUNDARY_DIRECTORY="+directory,
		"GOENV=untrusted", "GOWORK=untrusted", "GOTOOLCHAIN=auto", "GOPROXY=off", "GOSUMDB=off", "GOPRIVATE=*", "GONOSUMDB=*", "GONOPROXY=*", "GOFLAGS=-mod=mod")
	output, err := command.CombinedOutput()
	status := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			t.Fatalf("script did not exit normally: %v", err)
		}
		status = exitError.ExitCode()
	}
	if status != wantStatus {
		t.Fatalf("mode %s exit=%d want=%d output=%s", mode, status, wantStatus, output)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(log), directory
}
