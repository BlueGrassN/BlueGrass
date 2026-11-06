package winprotect

import "errors"

// A failed commit is still followed by abort; abort errors must not hide the
// original failure or be reported as a successful policy transition.
func transaction(begin, commit, abort, body func() error) (err error) {
	if err = begin(); err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			err = errors.Join(err, abort())
		}
	}()
	if err = body(); err != nil {
		return err
	}
	if err = commit(); err != nil {
		return err
	}
	done = true
	return nil
}
