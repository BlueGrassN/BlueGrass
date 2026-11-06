//go:build darwin

package tunengine

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/xtls/xray-core/common/platform"
	tun "github.com/xtls/xray-core/proxy/tun"
)

func openNativeTun(options *tun.Config) (tun.Tun, uint64, error) {
	if options == nil || options.Name != InterfaceName {
		return nil, 0, errors.New("TUN_ADAPTER_IDENTITY_MISMATCH")
	}
	// Upstream otherwise interprets this as an iOS NetworkExtension-owned fd.
	if platform.NewEnvFlag(platform.TunFdKey).GetValue(func() string { return "" }) != "" {
		return nil, 0, errors.New("TUN_INHERITED_FD_FORBIDDEN")
	}
	if os.Geteuid() != 0 {
		return nil, 0, errors.New("TUN_ADMIN_REQUIRED")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, 0, errors.New("TUN_ADAPTER_IDENTITY_UNAVAILABLE")
	}
	for _, iface := range interfaces {
		if iface.Name == InterfaceName {
			return nil, 0, errors.New("TUN_ADAPTER_ALREADY_EXISTS")
		}
	}
	// The kernel's fixed utun unit allocation is exclusive. A concurrent creator
	// wins or loses that allocation; NewTun cannot attach to an existing unit.
	device, err := tun.NewTun(darwinOwnedOptions(options))
	if err != nil {
		return nil, 0, err
	}
	name, nameErr := device.Name()
	index, indexErr := device.Index()
	if nameErr != nil || indexErr != nil || name != InterfaceName || index <= 0 {
		cleanup := device.Close()
		err := errors.Join(errors.New("TUN_ADAPTER_IDENTITY_MISMATCH"), nameErr, indexErr, cleanup)
		if cleanup != nil {
			return nil, 0, &nativeOpenError{err: err, device: device}
		}
		return nil, 0, err
	}
	return &checkedDarwinTun{Tun: device, index: index}, uint64(index), nil
}

type checkedDarwinTun struct {
	tun.Tun
	index int
	once  sync.Once
	err   error
}

func (d *checkedDarwinTun) Start() error {
	iface, err := net.InterfaceByName(InterfaceName)
	if err != nil || iface.Index != d.index {
		return errors.New("TUN_ADAPTER_IDENTITY_MISMATCH")
	}
	return d.Tun.Start()
}

func (d *checkedDarwinTun) Close() error {
	d.once.Do(func() {
		d.err = d.Tun.Close()
		deadline := time.Now().Add(2 * time.Second)
		for {
			interfaces, err := net.Interfaces()
			if err != nil {
				d.err = errors.Join(d.err, errors.New("TUN_CLEANUP_UNCONFIRMED"))
				return
			}
			present := false
			for _, iface := range interfaces {
				present = present || iface.Index == d.index && iface.Name == InterfaceName
			}
			if !present {
				return
			}
			if time.Now().After(deadline) {
				d.err = errors.Join(d.err, errors.New("TUN_INTERFACE_REMAINS"))
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	return d.err
}
