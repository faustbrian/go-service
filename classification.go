package service

import "reflect"

// walkClassification visits causes in standard errors.Is/As depth-first order.
// errors.Join can share causes, so native tree walks can expand a small finite
// DAG exponentially during command exit. Revisit identifiable causes only once;
// this is classification, not validation of arbitrary callback error graphs.
func walkClassification(err error, match func(error) bool) bool {
	seen := make(map[any]struct{})
	pending := []error{err}
	for len(pending) > 0 {
		index := len(pending) - 1
		current := pending[index]
		pending = pending[:index]
		if current == nil {
			continue
		}
		if key, identifiable := classificationKey(current); identifiable {
			if _, visited := seen[key]; visited {
				continue
			}
			seen[key] = struct{}{}
		}
		if match(current) {
			return true
		}
		switch wrapped := current.(type) { //nolint:errorlint // Inspect only this cause; recursive As would expand shared paths again.
		case interface{ Unwrap() []error }:
			children := wrapped.Unwrap()
			for index := len(children) - 1; index >= 0; index-- {
				pending = append(pending, children[index])
			}
		case interface{ Unwrap() error }:
			pending = append(pending, wrapped.Unwrap())
		}
	}
	return false
}

// A slice-backed error is not comparable, but copies retain their backing
// array, type and bounds. Keep those distinct from ordinary comparable errors
// so shared immutable slices can be classified without expanding every path.
func classificationKey(err error) (any, bool) {
	value := reflect.ValueOf(err)
	if value.Comparable() {
		return err, true
	}
	if value.Kind() == reflect.Slice {
		return struct {
			typeOf   reflect.Type
			data     uintptr
			length   int
			capacity int
		}{value.Type(), value.Pointer(), value.Len(), value.Cap()}, true
	}
	return nil, false
}

//nolint:errorlint // Match a single cause before the shared-node-aware walk unwraps it.
func classificationIs(err, target error) bool {
	if err == nil || target == nil {
		return err == nil && target == nil
	}
	comparable := reflect.ValueOf(target).Comparable()
	return walkClassification(err, func(current error) bool {
		if comparable && current == target {
			return true
		}
		matcher, ok := current.(interface{ Is(error) bool })
		return ok && matcher.Is(target)
	})
}

func classificationAs[T error](err error) (target T, found bool) {
	found = classificationInto(err, &target)
	return target, found
}

//nolint:errorlint // Preserve shallow As hooks without recursively revisiting shared causes.
func classificationInto(err error, target any) bool {
	value := reflect.ValueOf(target).Elem()
	return walkClassification(err, func(current error) bool {
		if reflect.TypeOf(current).AssignableTo(value.Type()) {
			value.Set(reflect.ValueOf(current))
			return true
		}
		matcher, ok := current.(interface{ As(any) bool })
		return ok && matcher.As(target)
	})
}
