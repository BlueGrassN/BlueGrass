package tunengine

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"strings"
)

// AdapterRepairError permits the repair UI to distinguish identity, privilege,
// platform and pending-removal failures without exposing native error strings.
type AdapterRepairError struct{ Code string }

func (e *AdapterRepairError) Error() string { return e.Code }

type adapterIdentity struct {
	id, guid, name, service string
	hardwareIDs             []string
}
type repairBackend interface {
	list() ([]adapterIdentity, error)
	remove(string) error
}

// Xray creates new adapters from the interface name alone.
func expectedAdapterGUID() string {
	return adapterGUIDForName(InterfaceName)
}

// Xray can reuse the same-name adapter created by the previous sing-tun core.
func legacyAdapterGUID() string {
	return adapterGUIDForName("wintun" + InterfaceName)
}

// MD5 reproduces the dependencies' identifiers, not authentication. Windows
// interprets the first three GUID fields as little-endian integers.
func adapterGUIDForName(name string) string {
	hash := md5.Sum([]byte(name))
	return fmt.Sprintf("{%08x-%04x-%04x-%x-%x}", binary.LittleEndian.Uint32(hash[:4]), binary.LittleEndian.Uint16(hash[4:6]), binary.LittleEndian.Uint16(hash[6:8]), hash[8:10], hash[10:])
}

// A product GUID is only one part of ownership; alias and driver identity must
// also be checked before reusing or removing an adapter.
func isOwnAdapterGUID(guid string) bool {
	return strings.EqualFold(guid, expectedAdapterGUID()) || strings.EqualFold(guid, legacyAdapterGUID())
}

func isOwnAdapter(d adapterIdentity) bool {
	if !isOwnAdapterGUID(d.guid) || d.name != InterfaceName || !strings.EqualFold(d.service, "Wintun") {
		return false
	}
	for _, hardware := range d.hardwareIDs {
		if strings.EqualFold(hardware, "Wintun") {
			return true
		}
	}
	return false
}
func repairAdapterWith(backend repairBackend) error {
	devices, err := backend.list()
	if err != nil {
		return &AdapterRepairError{Code: "ADAPTER_IDENTITY_UNAVAILABLE"}
	}
	var candidate *adapterIdentity
	for i := range devices {
		d := &devices[i]
		if !isOwnAdapterGUID(d.guid) {
			continue
		}
		if candidate != nil {
			return &AdapterRepairError{Code: "ADAPTER_IDENTITY_AMBIGUOUS"}
		}
		if !isOwnAdapter(*d) {
			return &AdapterRepairError{Code: "ADAPTER_IDENTITY_MISMATCH"}
		}
		candidate = d
	}
	if candidate == nil {
		return nil
	}
	if err = backend.remove(candidate.id); err != nil {
		return &AdapterRepairError{Code: "ADAPTER_REMOVAL_FAILED"}
	}
	devices, err = backend.list()
	if err != nil {
		return &AdapterRepairError{Code: "ADAPTER_REMOVAL_UNCONFIRMED"}
	}
	for _, d := range devices {
		if isOwnAdapterGUID(d.guid) {
			return &AdapterRepairError{Code: "ADAPTER_REMOVAL_PENDING"}
		}
	}
	return nil
}
