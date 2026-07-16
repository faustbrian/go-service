//go:build !windows

package goservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseFetchesMainBeforeComparingRemote(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	temporary := t.TempDir()
	remote := filepath.Join(temporary, "remote.git")
	releaseCheckout := filepath.Join(temporary, "release")
	updaterCheckout := filepath.Join(temporary, "updater")

	runGit(t, temporary, "init", "--bare", remote)
	runGit(t, temporary, "clone", remote, releaseCheckout)
	configureGit(t, releaseCheckout)
	runGit(t, releaseCheckout, "switch", "-c", "main")
	writeFile(t, filepath.Join(releaseCheckout, "CHANGELOG.md"), "# Changelog\n")
	runGit(t, releaseCheckout, "add", "CHANGELOG.md")
	runGit(t, releaseCheckout, "commit", "-m", "initial")
	runGit(t, releaseCheckout, "push", "-u", "origin", "main")

	runGit(t, temporary, "clone", remote, updaterCheckout)
	configureGit(t, updaterCheckout)
	runGit(t, updaterCheckout, "switch", "main")
	writeFile(t, filepath.Join(updaterCheckout, "remote-change"), "advanced\n")
	runGit(t, updaterCheckout, "add", "remote-change")
	runGit(t, updaterCheckout, "commit", "-m", "advance remote")
	runGit(t, updaterCheckout, "push", "origin", "main")

	command := exec.Command("bash", filepath.Join(root, "scripts", "release.sh"), "patch")
	command.Dir = releaseCheckout
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("release unexpectedly succeeded from stale main")
	}
	if !strings.Contains(string(output), "release requires main to match origin/main") {
		t.Fatalf("release did not reject stale main after fetching: %s", output)
	}
}

func TestReleaseRequiresUsableOpenPGPSigningKey(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	temporary := t.TempDir()
	remote := filepath.Join(temporary, "remote.git")
	checkout := filepath.Join(temporary, "release")

	runGit(t, temporary, "init", "--bare", remote)
	runGit(t, temporary, "clone", remote, checkout)
	configureGit(t, checkout)
	runGit(t, checkout, "switch", "-c", "main")
	writeFile(t, filepath.Join(checkout, "CHANGELOG.md"), "# Changelog\n\n## [0.0.1] - 2026-07-16\n")
	runGit(t, checkout, "add", "CHANGELOG.md")
	runGit(t, checkout, "commit", "-m", "prepare release")
	runGit(t, checkout, "push", "-u", "origin", "main")

	command := exec.Command("bash", filepath.Join(root, "scripts", "release.sh"), "patch")
	command.Dir = checkout
	command.Env = append(os.Environ(), "RELEASE_SIGNING_KEY=missing-test-key")
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("release unexpectedly accepted a missing signing key")
	}
	if !strings.Contains(string(output), "usable OpenPGP secret key") {
		t.Fatalf("release returned the wrong signing failure: %s", output)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()

	command := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("locate repository: %v", err)
	}

	return strings.TrimSpace(string(output))
}

func configureGit(t *testing.T, directory string) {
	t.Helper()

	runGit(t, directory, "config", "user.name", "Release Test")
	runGit(t, directory, "config", "user.email", "release@example.test")
}

func runGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()

	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
