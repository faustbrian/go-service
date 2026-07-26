package goservice_test

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

func TestProductionDependencyBoundaries(t *testing.T) {
	allowed := map[string][]string{
		modulePath:                  {modulePath},
		modulePath + "/service":     {modulePath + "/service"},
		modulePath + "/serverhttp":  {modulePath + "/serverhttp"},
		modulePath + "/healthhttp":  {modulePath + "/healthhttp", modulePath + "/service"},
		modulePath + "/integration": {modulePath + "/integration", modulePath + "/service"},
		modulePath + "/servicetest": {modulePath + "/service", modulePath + "/servicetest"},
	}

	for packagePath, permitted := range allowed {
		packages := listPackages(t, "-deps", packagePath)
		var nonStandard []string
		for _, current := range packages {
			if !current.Standard {
				nonStandard = append(nonStandard, current.ImportPath)
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
		modulePath + "/service",
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
	ImportPath string
	Standard   bool
}

func listPackages(t *testing.T, flag string, packagePath string) []listedPackage {
	t.Helper()

	arguments := []string{"list"}
	if flag != "" {
		arguments = append(arguments, flag)
	}
	arguments = append(arguments, "-json", packagePath)
	command := exec.Command("go", arguments...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("go list %s: %v", packagePath, err)
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
