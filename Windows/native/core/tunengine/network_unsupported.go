//go:build !windows && !darwin

package tunengine

import (
	"context"
	"errors"
)

func SystemNetwork(context.Context) (NetworkSnapshot, error) {
	return NetworkSnapshot{}, errors.New("WINDOWS_NETWORK_REQUIRED")
}

func readSystemAdapters(context.Context) ([]systemAdapter, error) {
	return nil, errors.New("WINDOWS_NETWORK_REQUIRED")
}
func readSystemRoutes(context.Context) ([]systemRoute, error) {
	return nil, errors.New("WINDOWS_NETWORK_REQUIRED")
}
