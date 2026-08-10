package service_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	implicitPolicyStackRule = "implicit or default resilience policy stack"
	policyAlgorithmRule     = "resilience algorithm ownership"
	policyImportRule        = "resilience policy-module import"
	globalRegistryRule      = "mutable package-global registry"
)

var resiliencePolicyModules = []string{
	"github.com/faustbrian/golib/pkg/adaptive-throttle",
	"github.com/faustbrian/golib/pkg/bulkhead",
	"github.com/faustbrian/golib/pkg/circuit-breaker",
	"github.com/faustbrian/golib/pkg/concurrency-limit",
	"github.com/faustbrian/golib/pkg/hedge",
	"github.com/faustbrian/golib/pkg/rate-limit",
	"github.com/faustbrian/golib/pkg/resilience",
	"github.com/faustbrian/golib/pkg/retry",
}

func TestServiceProductionPackagesDoNotOwnResiliencePolicy(t *testing.T) {
	packagePaths := []string{
		modulePath,
		modulePath + "/healthhttp",
		modulePath + "/integration",
		modulePath + "/serverhttp",
		modulePath + "/servicetest",
	}

	for _, packagePath := range packagePaths {
		listed := listPackages(t, "", packagePath)
		if len(listed) != 1 {
			t.Fatalf("go list %s returned %d packages", packagePath, len(listed))
		}
		current := listed[0]
		for _, name := range current.GoFiles {
			path := filepath.Join(current.Dir, name)
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, violation := range inspectResilienceArchitecture(fileSet, file) {
				t.Errorf("production package %s violates %s at %s: %s", packagePath, violation.rule, violation.location, violation.detail)
			}
		}
	}
}

func TestResilienceArchitectureRulesRejectRepresentativeViolations(t *testing.T) {
	fixtures := []struct {
		name     string
		source   string
		wantRule string
	}{
		{
			name:     "policy module import",
			source:   `package service; import "github.com/faustbrian/golib/pkg/retry/retryhttp"`,
			wantRule: policyImportRule,
		},
		{
			name:     "implicit stack",
			source:   "package service\nfunc defaultResilience() {}",
			wantRule: implicitPolicyStackRule,
		},
		{
			name:     "duplicated algorithm",
			source:   "package service\ntype circuitBreaker struct{}",
			wantRule: policyAlgorithmRule,
		},
		{
			name:     "global registry",
			source:   "package service\nvar policyRegistry = map[string]any{}",
			wantRule: globalRegistryRule,
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			fileSet := token.NewFileSet()
			file, err := parser.ParseFile(fileSet, fixture.name+".go", fixture.source, 0)
			if err != nil {
				t.Fatalf("parse representative violation: %v", err)
			}
			violations := inspectResilienceArchitecture(fileSet, file)
			for _, violation := range violations {
				if violation.rule == fixture.wantRule {
					return
				}
			}
			t.Fatalf("representative violation was not rejected by %q; got %+v", fixture.wantRule, violations)
		})
	}
}

type resilienceArchitectureViolation struct {
	rule     string
	location string
	detail   string
}

func inspectResilienceArchitecture(
	fileSet *token.FileSet,
	file *ast.File,
) []resilienceArchitectureViolation {
	var violations []resilienceArchitectureViolation
	report := func(rule string, node ast.Node, detail string) {
		violations = append(violations, resilienceArchitectureViolation{
			rule:     rule,
			location: fileSet.Position(node.Pos()).String(),
			detail:   detail,
		})
	}

	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err == nil && importsResiliencePolicyModule(path) {
			report(policyImportRule, imported, "service must receive caller-owned policies instead of importing "+path)
		}
	}

	for _, declaration := range file.Decls {
		switch current := declaration.(type) {
		case *ast.FuncDecl:
			inspectResilienceIdentifier(current.Name, report)
		case *ast.GenDecl:
			for _, specification := range current.Specs {
				switch spec := specification.(type) {
				case *ast.TypeSpec:
					inspectResilienceIdentifier(spec.Name, report)
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						inspectResilienceIdentifier(name, report)
					}
					if current.Tok == token.VAR {
						inspectPackageGlobal(spec, report)
					}
				}
			}
		}
	}

	return violations
}

func inspectResilienceIdentifier(
	identifier *ast.Ident,
	report func(string, ast.Node, string),
) {
	if namesImplicitPolicyStack(identifier.Name) {
		report(implicitPolicyStackRule, identifier, identifier.Name+" would make policy composition service-owned")
	}
	if namesResilienceAlgorithm(identifier.Name) {
		report(policyAlgorithmRule, identifier, identifier.Name+" duplicates an algorithm owned by a focused resilience module")
	}
}

func inspectPackageGlobal(spec *ast.ValueSpec, report func(string, ast.Node, string)) {
	mutableCollection := mutableRegistryType(spec.Type)
	for _, value := range spec.Values {
		mutableCollection = mutableCollection || mutableRegistryValue(value)
	}
	for _, name := range spec.Names {
		if mutableCollection || namesRegistry(name.Name) {
			report(globalRegistryRule, name, name.Name+" would hide caller-owned policy or registration state")
		}
	}
}

func importsResiliencePolicyModule(importPath string) bool {
	for _, module := range resiliencePolicyModules {
		if importPath == module || strings.HasPrefix(importPath, module+"/") {
			return true
		}
	}
	return false
}

func namesImplicitPolicyStack(name string) bool {
	normalized := normalizeArchitectureIdentifier(name)
	return strings.Contains(normalized, "defaultresilience") ||
		strings.HasSuffix(normalized, "resiliencestack") ||
		strings.HasSuffix(normalized, "policystack") ||
		strings.HasSuffix(normalized, "policyset") ||
		strings.HasPrefix(normalized, "defaultpolicies")
}

func namesResilienceAlgorithm(name string) bool {
	normalized := normalizeArchitectureIdentifier(name)
	if strings.Contains(normalized, "retryafter") {
		return false
	}
	for _, owned := range []string{
		"adaptivethrottle",
		"backoff",
		"bulkhead",
		"circuitbreaker",
		"concurrencylimit",
		"ratelimit",
		"resiliencebudget",
		"retry",
	} {
		if strings.Contains(normalized, owned) {
			return true
		}
	}
	return strings.Contains(normalized, "hedge") || strings.Contains(normalized, "hedging")
}

func namesRegistry(name string) bool {
	normalized := normalizeArchitectureIdentifier(name)
	return strings.Contains(normalized, "registry") ||
		strings.Contains(normalized, "registries") ||
		strings.HasSuffix(normalized, "policies") ||
		strings.HasSuffix(normalized, "policybyname")
}

func normalizeArchitectureIdentifier(name string) string {
	return strings.ToLower(strings.ReplaceAll(name, "_", ""))
}

func mutableRegistryValue(expression ast.Expr) bool {
	switch current := expression.(type) {
	case *ast.CompositeLit:
		return mutableRegistryType(current.Type)
	case *ast.CallExpr:
		identifier, ok := current.Fun.(*ast.Ident)
		return ok && (identifier.Name == "make" || identifier.Name == "new") &&
			len(current.Args) != 0 && mutableRegistryType(current.Args[0])
	default:
		return false
	}
}

func mutableRegistryType(expression ast.Expr) bool {
	switch current := expression.(type) {
	case *ast.MapType, *ast.ChanType:
		return true
	case *ast.ArrayType:
		return current.Len == nil
	case *ast.StarExpr:
		return mutableRegistryType(current.X)
	case *ast.SelectorExpr:
		packageName, ok := current.X.(*ast.Ident)
		return ok && packageName.Name == "sync" && current.Sel.Name == "Map"
	default:
		return false
	}
}
