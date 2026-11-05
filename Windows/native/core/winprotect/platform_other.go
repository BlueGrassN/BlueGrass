//go:build !windows || (!amd64 && !arm64)

package winprotect

import "errors"

func New(serviceExe, uiExe string) (*Manager, error) {
	return nil, errors.New("WFP protection requires 64-bit Windows")
}
func Repair() error { return errors.New("WFP protection requires 64-bit Windows") }
