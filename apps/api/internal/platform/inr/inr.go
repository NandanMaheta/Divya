// Package inr formats integer paise as Indian-grouped rupee strings (integer-only, no floats).
package inr //nolint:unparam,revive,unused // S01 compile skeleton; removed as slices land

import "errors"

var errNotImplemented = errors.New("inr: not implemented")

// Format renders paise as e.g. "₹1,23,456.78".
func Format(paise int64) string { panic(errNotImplemented) }
