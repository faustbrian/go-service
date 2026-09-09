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
