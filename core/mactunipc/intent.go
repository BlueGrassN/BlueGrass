package mactunipc

import (
	"errors"
	"io"
	"os"
)

func readIntent(open func(string) (io.ReadCloser, error)) (*Intent, error) {
	f, err := open(IntentPath)
	if errors.Is(err, os.ErrNotExist) {
		// No shipped TUN version needs migration. An unsupported development
		// marker cannot be silently treated as permission to bypass recovery.
		legacy, legacyErr := open(RuntimeDirectory + "/intent.json")
		if legacyErr == nil {
			_ = legacy.Close()
			return nil, ErrRecovery
		}
		if errors.Is(legacyErr, os.ErrNotExist) {
			return nil, nil
		}
		return nil, ErrRecovery
	}
	if err != nil {
		return nil, ErrRecovery
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil {
		return nil, ErrRecovery
	}
	i, err := DecodeIntent(b)
	if err != nil {
		return nil, err
	}
	return &i, nil
}
