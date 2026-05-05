package repository

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelErrorsDistinct(t *testing.T) {
	all := []error{ErrNotFound, ErrConflict, ErrNotImplemented, ErrInvalidInput, ErrBackendUnavailable}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("%v should not be %v", a, b)
			}
		}
	}
}

func TestErrorWrapping(t *testing.T) {
	wrapped := fmt.Errorf("collection abc: %w", ErrNotFound)
	if !errors.Is(wrapped, ErrNotFound) {
		t.Fatalf("errors.Is should see ErrNotFound through wrap")
	}
}
