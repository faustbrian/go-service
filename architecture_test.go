package service_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

const modulePath = "github.com/faustbrian/golib/pkg/service"

func TestRootPackageIsService(t *testing.T) {
	t.Parallel()

	listed := listPackages(t, "", modulePath)
	if len(listed) != 1 {
		t.Fatalf("go list %s returned %d packages", modulePath, len(listed))
	}
	if name := listed[0].Name; name != "service" {
		t.Fatalf("root package name = %q, want service", name)
	}
}

func TestProductionDependencyBoundaries(t *testing.T) {
	standard := make(map[string]struct{})
	for _, packageInfo := range listPackages(t, "", "std") {
		standard[packageInfo.ImportPath] = struct{}{}
	}
	allowed := map[string][]string{
		modulePath: {
			"github.com/faustbrian/golib/pkg/cli",
			"github.com/faustbrian/golib/pkg/correlation",
			"github.com/faustbrian/golib/pkg/correlation/http",
			modulePath + "/healthhttp",
			modulePath + "/serverhttp",
		},
		modulePath + "/serverhttp": {
			"github.com/faustbrian/golib/pkg/correlation",
			"github.com/faustbrian/golib/pkg/correlation/http",
		},
		modulePath + "/healthhttp":  {},
		modulePath + "/integration": {modulePath},
		modulePath + "/servicetest": {modulePath},
	}

	for packagePath, permitted := range allowed {
		packages := listPackages(t, "", packagePath)
		if len(packages) != 1 {
			t.Fatalf("go list %s returned %d packages", packagePath, len(packages))
		}
		var nonStandard []string
		for _, imported := range packages[0].Imports {
			if _, ok := standard[imported]; !ok {
				nonStandard = append(nonStandard, imported)
			}
		}
		slices.Sort(nonStandard)
		if !slices.Equal(nonStandard, permitted) {
			t.Errorf(
				"package %s has non-standard dependencies %v, want %v",
				packagePath,
				nonStandard,
				permitted,
			)
		}
	}
}

func TestProductionPackagesHaveNoInitializers(t *testing.T) {
	packages := []string{
		modulePath,
		modulePath + "/serverhttp",
		modulePath + "/healthhttp",
		modulePath + "/integration",
		modulePath + "/servicetest",
	}
	for _, packagePath := range packages {
		listed := listPackages(t, "", packagePath)
		if len(listed) != 1 {
			t.Fatalf("go list %s returned %d packages", packagePath, len(listed))
		}
		current := listed[0]
		for _, name := range current.GoFiles {
			path := filepath.Join(current.Dir, name)
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if ok && function.Recv == nil && function.Name.Name == "init" {
					t.Errorf("production package %s declares init in %s", packagePath, path)
				}
			}
		}
	}
}

type listedPackage struct {
	Dir        string
	GoFiles    []string
	Imports    []string
	ImportPath string
	Name       string
}

func listPackages(t *testing.T, flag string, packagePath string) []listedPackage {
	t.Helper()

	arguments := []string{"list"}
	if flag != "" {
		arguments = append(arguments, flag)
	}
	arguments = append(arguments, "-json", packagePath)
	command := exec.Command("go", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go list %s: %v\n%s", packagePath, err, output)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	var packages []listedPackage
	for {
		var current listedPackage
		err := decoder.Decode(&current)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("decode go list %s: %v", packagePath, err)
		}
		packages = append(packages, current)
	}

	return packages
}
