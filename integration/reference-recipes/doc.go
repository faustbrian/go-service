// Package referencerecipes contains executable, non-production composition
// recipes that exercise released Golib APIs without repository-local imports.
// There is no production entrypoint or backend here; test fixtures own listeners,
// bounded synchronization and in-memory handoff, not durable broker semantics.
package referencerecipes
