package nativehost

import (
	"errors"

	"bluegrass/native/core/winproxy"
)

// repairProxy never forgets an owned backup on an arbitrary restore failure.
// A successful return also requires that no recovery record remains active.
func repairProxy(manager interface {
	Restore() error
	Relinquish() error
	State() (bool, error)
}) (preserved bool, err error) {
	if err = manager.Restore(); err != nil {
		if !errors.Is(err, winproxy.ErrOwnershipLost) {
			return false, err
		}
		if err = manager.Relinquish(); err != nil {
			return false, err
		}
		preserved = true
	}
	active, err := manager.State()
	if err != nil {
		return false, err
	}
	if active {
		return false, winproxy.ErrRecoveryRequired
	}
	return preserved, nil
}
