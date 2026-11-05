//go:build !windows

package tunengine

import (
	"errors"
	tun "github.com/xtls/xray-core/proxy/tun"
)

func openNativeTun(*tun.Config) (tun.Tun, uint64, error) {
	return nil, 0, errors.New("WINDOWS_BUILD_REQUIRED")
}
