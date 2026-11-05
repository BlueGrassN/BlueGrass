//go:build windows

package tunengine

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const networkClassGUIDString = "{4d36e972-e325-11ce-bfc1-08002be10318}"

var networkClassGUID = windows.GUID{Data1: 0x4d36e972, Data2: 0xe325, Data3: 0x11ce, Data4: [8]byte{0xbf, 0xc1, 0x08, 0x00, 0x2b, 0xe1, 0x03, 0x18}}

type windowsRepair struct{}

// RepairAdapter removes only a uniquely identified product adapter, accepting
// both new Xray and reused legacy GUIDs. The caller must already have stopped
// BlueGrassService and handled WFP/proxy repair.
// This performs a device-instance DIF_REMOVE, never a shared driver removal.
func RepairAdapter() error { return repairAdapterWith(windowsRepair{}) }

func readAdapterIdentity(set windows.DevInfo, device *windows.DevInfoData) (adapterIdentity, error) {
	handle, err := set.OpenDevRegKey(device, windows.DICS_FLAG_GLOBAL, 0, windows.DIREG_DRV, windows.KEY_QUERY_VALUE)
	if err != nil {
		return adapterIdentity{}, err
	}
	key := registry.Key(handle)
	defer key.Close()
	guid, _, err := key.GetStringValue("NetCfgInstanceId")
	if err != nil {
		return adapterIdentity{}, err
	}
	if !isOwnAdapterGUID(guid) {
		return adapterIdentity{guid: guid}, nil
	}
	incomplete := adapterIdentity{guid: guid}
	id, err := windows.SetupDiGetDeviceInstanceId(set, device)
	if err != nil {
		return incomplete, err
	}
	// The network Connection key proves the actual adapter name; PnP friendly
	// names are insufficient and may not equal the interface alias.
	connection, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Network\`+networkClassGUIDString+`\`+guid+`\Connection`, registry.QUERY_VALUE)
	if err != nil {
		return incomplete, err
	}
	name, _, err := connection.GetStringValue("Name")
	_ = connection.Close()
	if err != nil {
		return incomplete, err
	}
	serviceValue, err := set.DeviceRegistryProperty(device, windows.SPDRP_SERVICE)
	if err != nil {
		return incomplete, err
	}
	hardwareValue, err := set.DeviceRegistryProperty(device, windows.SPDRP_HARDWAREID)
	if err != nil {
		return incomplete, err
	}
	serviceName, _ := serviceValue.(string)
	hardware, _ := hardwareValue.([]string)
	return adapterIdentity{id: id, guid: guid, name: name, service: serviceName, hardwareIDs: hardware}, nil
}
func enumerateAdapterIdentities(visit func(windows.DevInfo, *windows.DevInfoData, adapterIdentity) error) error {
	set, err := windows.SetupDiGetClassDevsEx(&networkClassGUID, "", 0, 0, 0, "")
	if err != nil {
		return err
	}
	defer set.Close()
	for i := 0; ; i++ {
		device, err := set.EnumDeviceInfo(i)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return nil
		}
		if err != nil {
			return err
		}
		identity, err := readAdapterIdentity(set, device)
		if err != nil && isOwnAdapterGUID(identity.guid) {
			return err
		}
		// Some unrelated software device instances have no driver key yet.
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_INVALID_DATA) {
			continue
		}
		if err != nil {
			return err
		}
		if err = visit(set, device, identity); err != nil {
			return err
		}
	}
}
func (windowsRepair) list() ([]adapterIdentity, error) {
	var result []adapterIdentity
	err := enumerateAdapterIdentities(func(_ windows.DevInfo, _ *windows.DevInfoData, d adapterIdentity) error {
		result = append(result, d)
		return nil
	})
	return result, err
}
func (windowsRepair) remove(id string) error {
	removed := false
	err := enumerateAdapterIdentities(func(set windows.DevInfo, device *windows.DevInfoData, d adapterIdentity) error {
		if d.id != id {
			return nil
		}
		if removed || !isOwnAdapter(d) {
			return errors.New("ADAPTER_IDENTITY_CHANGED")
		}
		params := windows.RemoveDeviceParams{ClassInstallHeader: *windows.MakeClassInstallHeader(windows.DIF_REMOVE), Scope: windows.DI_REMOVEDEVICE_GLOBAL}
		if err := set.SetClassInstallParams(device, &params.ClassInstallHeader, uint32(unsafe.Sizeof(params))); err != nil {
			return err
		}
		if err := set.CallClassInstaller(windows.DIF_REMOVE, device); err != nil {
			return err
		}
		removed = true
		install, err := set.DeviceInstallParams(device)
		if err != nil {
			return err
		}
		if install.Flags&(windows.DI_NEEDREBOOT|windows.DI_NEEDRESTART) != 0 {
			return errors.New("ADAPTER_REMOVAL_REBOOT_REQUIRED")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !removed {
		return errors.New("ADAPTER_IDENTITY_CHANGED")
	}
	return nil
}
