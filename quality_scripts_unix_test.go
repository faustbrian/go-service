//go:build !windows

package goservice_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestQualityScriptsDoNotRequireRipgrep(t *testing.T) {
	root := repositoryRoot(t)
	path := restrictedToolPath(t)

	for _, test := range []struct {
		name      string
		script    string
		arguments []string
	}{
		{name: "safety", script: "check-go-safety.sh"},
		{name: "fuzz", script: "check-fuzz.sh", arguments: []string{"1x"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(
				filepath.Join(root, "scripts", test.script),
				test.arguments...,
			)
			command.Dir = root
			command.Env = environmentWithPath(path)
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("%s without ripgrep: %v\n%s", test.script, err, output)
			}
			if strings.Contains(string(output), "command not found") {
				t.Fatalf("%s silently missed a tool: %s", test.script, output)
			}
		})
	}
}

func TestSafetyScriptRejectsViolationWithoutRipgrep(t *testing.T) {
	root := repositoryRoot(t)
	tests := map[string]string{
		"unsafe import":      "package violation\n\nimport \"unsafe\"\n\nvar _ unsafe.Pointer\n",
		"cgo import block":   "package violation\n\nimport (\n\t\"C\"\n)\n",
		"linkname directive": "package violation\n\n//go:linkname local target\nfunc local()\n",
	}
	for name, source := range tests {
		t.Run(name, func(t *testing.T) {
			temporary := t.TempDir()
			writeFile(t, filepath.Join(temporary, "go.mod"), "module example.test/violation\n\ngo 1.25\n")
			writeFile(t, filepath.Join(temporary, "violation.go"), source)

			command := exec.Command(filepath.Join(root, "scripts", "check-go-safety.sh"))
			command.Dir = temporary
			command.Env = environmentWithPath(restrictedToolPath(t))
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("safety check accepted %s", name)
			}
			if !strings.Contains(string(output), "GO-SAFETY-1 violation") {
				t.Fatalf("safety check returned the wrong failure: %s", output)
			}
		})
	}
}

func restrictedToolPath(t *testing.T) string {
	t.Helper()

	goExecutable, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("locate go executable: %v", err)
	}

	return filepath.Dir(goExecutable) + ":/usr/bin:/bin"
}

func environmentWithPath(path string) []string {
	environment := make([]string, 0, len(os.Environ()))
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "PATH=") {
			environment = append(environment, variable)
		}
	}

	return append(environment, "PATH="+path)
}
