// Package service coordinates service construction, startup, supervision,
// draining, and shutdown.
//
// Applications may compose this root lifecycle directly or use the focused
// serverhttp and healthhttp subpackages. Importing service has no side effects.
package service
