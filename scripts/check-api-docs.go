//go:build ignore

// Command check-api-docs verifies documentation for exported API declarations.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	root := "."
	if len(os.Args) == 2 {
		root = os.Args[1]
	}
	var failures []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") ||
				entry.Name() == "examples" || entry.Name() == "vendor") {
				return filepath.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			path == filepath.Join(root, "scripts", "check-api-docs.go") {
			return nil
		}

		fileSet := token.NewFileSet()
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if parseErr != nil {
			return parseErr
		}
		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				if typed.Name.IsExported() && receiverIsExported(typed) && typed.Doc == nil {
					failures = append(failures, location(fileSet, typed.Pos(), typed.Name.Name))
				}
			case *ast.GenDecl:
				inspectGeneral(fileSet, typed, &failures)
			}
		}

		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, failure := range failures {
		fmt.Fprintln(os.Stderr, failure+": exported API lacks documentation")
	}
	if len(failures) > 0 {
		os.Exit(1)
	}
}

func receiverIsExported(function *ast.FuncDecl) bool {
	if function.Recv == nil {
		return true
	}
	receiver := function.Recv.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	identifier, ok := receiver.(*ast.Ident)

	return ok && identifier.IsExported()
}

func inspectGeneral(fileSet *token.FileSet, declaration *ast.GenDecl, failures *[]string) {
	for _, specification := range declaration.Specs {
		switch typed := specification.(type) {
		case *ast.TypeSpec:
			if !typed.Name.IsExported() {
				continue
			}
			if declaration.Doc == nil && typed.Doc == nil {
				*failures = append(*failures, location(fileSet, typed.Pos(), typed.Name.Name))
			}
			fields, ok := typed.Type.(*ast.StructType)
			if !ok {
				if contract, isInterface := typed.Type.(*ast.InterfaceType); isInterface {
					inspectFields(fileSet, contract.Methods, failures)
				}

				continue
			}
			inspectFields(fileSet, fields.Fields, failures)
		case *ast.ValueSpec:
			for _, name := range typed.Names {
				if name.IsExported() && declaration.Doc == nil && typed.Doc == nil {
					*failures = append(*failures, location(fileSet, name.Pos(), name.Name))
				}
			}
		}
	}
}

func inspectFields(fileSet *token.FileSet, fields *ast.FieldList, failures *[]string) {
	for _, field := range fields.List {
		for _, name := range field.Names {
			if name.IsExported() && field.Doc == nil && field.Comment == nil {
				*failures = append(*failures, location(fileSet, name.Pos(), name.Name))
			}
		}
	}
}

func location(fileSet *token.FileSet, position token.Pos, name string) string {
	return fmt.Sprintf("%s: %s", fileSet.Position(position), name)
}
