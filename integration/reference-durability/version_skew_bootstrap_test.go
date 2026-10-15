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

func TestVersionSkewPersistsUnconsumedDataAndAcknowledgedGroupCheckpoint(t *testing.T) {
	t.Parallel()
	for mode, status := range map[string]int{
		"persistence-retained": 0, "persistence-resp3": 0,
		"persistence-empty": 1, "persistence-reset-group": 1,
		"persistence-missing-group": 1, "persistence-redelivered": 1, "persistence-changed-data": 1,
	} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			log, directory := runVersionSkewBoundary(t, mode, status)
			if !strings.Contains(log, "docker=kill task-container-id\n") {
				t.Fatal("scenario did not replace the backend")
			}
			consumed := strings.Contains(log, "-prefix upgrade-sentinel -consumer")
			if consumed != (status == 0) {
				t.Fatal("lost persisted state reached a worker that could recreate its group")
			}
			reason := "acknowledgement persistence passed"
			switch mode {
			case "persistence-empty":
				reason = "mixed retained entries=0, want one sentinel"
			case "persistence-reset-group", "persistence-missing-group":
				reason = "mixed missing or invalid persisted group checkpoint"
			case "persistence-redelivered":
				reason = "mixed missing or unexpected sentinel entries"
			case "persistence-changed-data":
				reason = "mixed persisted entries changed on backend replacement"
			}
			if !strings.Contains(log, reason) {
				t.Fatalf("scenario did not reach its expected persistence outcome: %s", log)
			}
			for _, name := range []string{"container", "volume"} {
				if _, err := os.Stat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("owned %s survived scenario cleanup", name)
				}
			}
		})
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
if [ "$1 $2 $3 $4" = "-mode identity -queue-version $built" ]; then
 printf '{"queue_version":"%s"}\n' "$built"
 exit 0
fi
case "$BOUNDARY_MODE" in persistence-*) exec "$BOUNDARY_DIRECTORY/bin/scenario" "$@" ;; esac
exit 96
CONSUMER
  chmod +x consumer ;;
esac
`,
		"docker": `#!/bin/sh
printf 'docker=%s\n' "$*" >>"$BOUNDARY_LOG"
case "$BOUNDARY_MODE" in persistence-*) exec "$BOUNDARY_DIRECTORY/bin/backend" "$@" ;; esac
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
		"scenario": `#!/usr/bin/env python3
import argparse, json, os, signal, time
p = argparse.ArgumentParser()
for key in ('mode','queue-version','address','stream','prefix','consumer','ready','release','timeout','count','first'):
 p.add_argument('--'+key, '-'+key)
p.add_argument('-reclaim', action='store_true')
a = p.parse_args()
ids = [a.prefix+'-0']
if a.mode == 'publish':
 if a.prefix.endswith('-sentinel'):
  open(os.path.join(os.environ['BOUNDARY_DIRECTORY'], a.prefix), 'w').write('persisted-envelope')
elif a.mode == 'hold':
 signal.signal(signal.SIGTERM, lambda *_: exit(0))
 with open(a.ready, 'w') as f: json.dump(dict(ids=ids, acknowledged=0, pending=1, lag=0),f)
 signal.pause()
elif a.mode == 'compete':
 with open(a.ready, 'w') as f: json.dump(dict(ids=ids),f)
 while not os.path.exists(a.release): time.sleep(.01)
 ids = ['mixed-'+str(i) for i in (range(4) if a.consumer == 'mixed-old' else range(4,8))]
elif a.mode == 'verify-ack': ids=[]
print(json.dumps(dict(ids=ids, acknowledged=len(ids), reclaimed=int(a.reclaim), pending=0, lag=0)))
`,
		"backend": `#!/usr/bin/env python3
import json, os, sys
d = os.environ['BOUNDARY_DIRECTORY']
a = sys.argv[1:]
mode = os.environ['BOUNDARY_MODE']
def put(name, value):
 with open(os.path.join(d,name),'w') as f: f.write(value)
if a[:2] == ['volume','inspect']:
 if a[2:3] == ['--format']: print(open(os.path.join(d,'owner')).read())
 else: sys.exit(1)
elif a[:2] == ['volume','create']:
 put('owner',a[3].split('=',1)[1]); put('volume','owned')
elif a[:2] == ['volume','rm']: os.remove(os.path.join(d,'volume'))
elif a[:2] == ['image','inspect']: pass
elif a[0] == 'create':
 path = os.path.join(d,'generation')
 generation = int(open(path).read())+1 if os.path.exists(path) else 1
 put('generation',str(generation)); put('container','owned'); print('task-container-id')
elif a[0] == 'rm': os.remove(os.path.join(d,'container'))
elif a[0] == 'port': print('127.0.0.1:6379')
elif a[0] == 'exec':
 command = a[3:]
 if command == ['ping']: print('PONG')
 elif command == ['INFO','server']: print('valkey_version:9.1.0')
 else:
  command = command[1:]
  operation = command[0]
  stream = command[2] if operation == 'XINFO' else command[1]
  replaced = open(os.path.join(d,'generation')).read() == '2'
  cohort = stream.rsplit('-',1)[1]
  corrupted = replaced and cohort == 'mixed'
  present = os.path.exists(os.path.join(d,cohort+'-sentinel'))
  if operation == 'XLEN': print(0 if corrupted and mode == 'persistence-empty' else int(present))
  elif operation == 'XPENDING': print('0\n\n\n')
  elif operation == 'XRANGE':
   entries = [['200-0',['payload',cohort+'-sentinel-0']]] if present else []
   if corrupted and mode == 'persistence-redelivered': entries += [['100-0',['payload',cohort+'-0']]]
   if corrupted and mode == 'persistence-changed-data': entries[0][1][1] = 'altered'
   print(json.dumps(entries))
  elif operation == 'XINFO':
   fields = ['name','released-queue-skew','consumers',1,'pending',0,'last-delivered-id',
    '0-0' if corrupted and mode == 'persistence-reset-group' else '100-0','entries-read',1,'lag',1]
   groups = [dict(zip(fields[::2],fields[1::2])) if mode == 'persistence-resp3' else fields]
   if corrupted and mode == 'persistence-missing-group': groups = []
   print(json.dumps(groups))
`,
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(source), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
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
	return string(log) + "\nscript-output:\n" + string(output), directory
}
