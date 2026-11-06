//go:build !android && !linux

package bridge

import tun "github.com/xtls/xray-core/proxy/tun"

func openMobileTun(_ int) (tun.Tun, error) { return nil, errUnsupported }
