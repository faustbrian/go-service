//go:build ignore

// Command check-go-safety rejects forbidden production Go features.
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: check-go-safety <module-root>")
		os.Exit(2)
	}

	var violations []string
	err := filepath.WalkDir(os.Args[1], func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != os.Args[1] && (strings.HasPrefix(entry.Name(), ".") ||
				entry.Name() == "vendor") {
				return filepath.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		fileSet := token.NewFileSet()
		file, parseErr := parser.ParseFile(fileSet, path, nil, parser.ParseComments)
		if parseErr != nil {
			return fmt.Errorf("parse %s: %w", path, parseErr)
		}
		for _, imported := range file.Imports {
			name, unquoteErr := strconv.Unquote(imported.Path.Value)
			if unquoteErr != nil {
				return fmt.Errorf("parse import in %s: %w", path, unquoteErr)
			}
			if name == "unsafe" || name == "C" {
				violations = append(violations, fmt.Sprintf(
					"%s:%d: forbidden import %q",
					path,
					fileSet.Position(imported.Pos()).Line,
					name,
				))
			}
		}
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if strings.HasPrefix(strings.TrimSpace(comment.Text), "//go:linkname") {
					violations = append(violations, fmt.Sprintf(
						"%s:%d: forbidden go:linkname directive",
						path,
						fileSet.Position(comment.Pos()).Line,
					))
				}
			}
		}

		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if len(violations) == 0 {
		return
	}
	for _, violation := range violations {
		fmt.Fprintln(os.Stderr, violation)
	}
	fmt.Fprintln(os.Stderr, "GO-SAFETY-1 violation")
	os.Exit(1)
}
