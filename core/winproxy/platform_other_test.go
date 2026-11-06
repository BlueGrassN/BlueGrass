//go:build !windows

package winproxy

import (
	"errors"
	"testing"
)

func TestNonWindowsConstructorIsExplicitlyUnsupported(t *testing.T) {
	m, err := New("unused")
	if m != nil || !errors.Is(err, ErrUnsupported) {
		t.Fatalf("got %v,%v", m, err)
	}
}
