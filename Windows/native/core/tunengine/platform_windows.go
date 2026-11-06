//go:build windows

package tunengine

import (
	"errors"
	"net/netip"
	"strings"
	"sync"
	"time"

	tun "github.com/xtls/xray-core/proxy/tun"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.zx2c4.com/wireguard/windows/tunnel/winipcfg"
)

// Wintun 0.14.1 opens by this property, not by the network Connection alias.
var wintunNameProperty = windows.DEVPROPKEY{
	FmtID: windows.DEVPROPGUID{Data1: 0x3361c968, Data2: 0x2f2e, Data3: 0x4660, Data4: [8]byte{0xb4, 0x7e, 0x69, 0x9c, 0xdc, 0x4c, 0x32, 0xb9}},
	PID:   windows.DEVPROPID_FIRST_USABLE + 1,
}

type nativeAdapterRecord struct {
	adapterIdentity
	wintunName string
}

func selectNativeAdapter(devices []nativeAdapterRecord) (*nativeAdapterRecord, error) {
	var candidate *nativeAdapterRecord
	for i := range devices {
		d := &devices[i]
		if !isOwnAdapterGUID(d.guid) && !strings.EqualFold(d.name, InterfaceName) && !strings.EqualFold(d.wintunName, InterfaceName) {
			continue
		}
		if candidate != nil {
			return nil, errors.New("TUN_ADAPTER_IDENTITY_AMBIGUOUS")
		}
		candidate = d
	}
	if candidate != nil && (!isOwnAdapter(candidate.adapterIdentity) || !strings.EqualFold(candidate.wintunName, InterfaceName)) {
		return nil, errors.New("TUN_ADAPTER_IDENTITY_MISMATCH")
	}
	return candidate, nil
}

func nativeAdapterIdentities() ([]nativeAdapterRecord, error) {
	set, err := windows.SetupDiGetClassDevsEx(&networkClassGUID, "", 0, windows.DIGCF_PRESENT, 0, "")
	if err != nil {
		return nil, err
	}
	defer set.Close()
	var result []nativeAdapterRecord
	for i := 0; ; i++ {
		device, err := set.EnumDeviceInfo(i)
		if errors.Is(err, windows.ERROR_NO_MORE_ITEMS) {
			return result, nil
		}
		if err != nil {
			return nil, err
		}
		value, err := windows.SetupDiGetDeviceProperty(set, device, &wintunNameProperty)
		if err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) {
			return nil, err
		}
		storedName, _ := value.(string)
		d, err := readAdapterIdentity(set, device)
		if err != nil {
			if !isOwnAdapterGUID(d.guid) && !strings.EqualFold(storedName, InterfaceName) && (errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_INVALID_DATA)) {
				continue
			}
			return nil, err
		}
		// Also inspect foreign aliases so creation cannot rename an unrelated
		// same-name device. Repair only reads full data for product GUIDs.
		if !isOwnAdapterGUID(d.guid) && d.guid != "" {
			key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Network\`+networkClassGUIDString+`\`+d.guid+`\Connection`, registry.QUERY_VALUE)
			if err == nil {
				d.name, _, err = key.GetStringValue("Name")
				_ = key.Close()
			}
			if err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
				return nil, err
			}
		}
		result = append(result, nativeAdapterRecord{adapterIdentity: d, wintunName: storedName})
	}
}

func openNativeTun(options *tun.Config) (tun.Tun, uint64, error) {
	if options == nil || options.Name != InterfaceName {
		return nil, 0, errors.New("TUN_ADAPTER_IDENTITY_MISMATCH")
	}
	devices, err := nativeAdapterIdentities()
	if err != nil {
		return nil, 0, errors.New("TUN_ADAPTER_IDENTITY_UNAVAILABLE")
	}
	existing, err := selectNativeAdapter(devices)
	if err != nil {
		return nil, 0, err
	}
	expectedGUID := expectedAdapterGUID()
	if existing != nil {
		expectedGUID = existing.guid
	}
	checked := &checkedNativeTun{guid: expectedGUID}
	for _, value := range options.AutoSystemRoutingTable {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, 0, err
		}
		checked.ownedRoutes = append(checked.ownedRoutes, prefix.Masked())
	}
	for _, value := range options.Gateway {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, 0, err
		}
		checked.ownedAddresses = append(checked.ownedAddresses, prefix.Addr())
	}
	if err := loadWintun(); err != nil {
		return nil, 0, err
	}
	checked.Tun, err = tun.NewTun(options)
	if err != nil {
		return nil, 0, err
	}
	if err = checked.validateIdentity(false); err != nil {
		if cleanup := checked.Close(); cleanup != nil {
			// Never publish an unverified LUID to the host firewall policy.
			return nil, 0, &nativeOpenError{err: errors.Join(err, cleanup), device: checked}
		}
		return nil, 0, err
	}
	return checked, uint64(checked.luid), nil
}

func nativeLUIDFromIndex(index int) (winipcfg.LUID, error) {
	if index <= 0 || uint64(index) > uint64(^uint32(0)) {
		return 0, errors.New("TUN_LUID_UNAVAILABLE")
	}
	luid, err := winipcfg.LUIDFromIndex(uint32(index))
	if err != nil || luid == 0 {
		return 0, errors.New("TUN_LUID_UNAVAILABLE")
	}
	return luid, nil
}

func ownedTunLUID(index int) (winipcfg.LUID, error) {
	luid, err := nativeLUIDFromIndex(index)
	if err != nil {
		return 0, err
	}
	guid, err := luid.GUID()
	if err != nil || !isOwnAdapterGUID(guid.String()) {
		return 0, errors.New("TUN_ADAPTER_IDENTITY_MISMATCH")
	}
	checked := &checkedNativeTun{luid: luid, guid: guid.String()}
	if err := checked.validateIdentity(false); err != nil {
		return 0, err
	}
	return luid, nil
}

type nativeCleanupSnapshot struct {
	routes                          []winipcfg.MibIPforwardRow2
	addresses                       []winipcfg.MibUnicastIPAddressRow
	interfacePresent, dnsEnumerated bool
	dns                             []netip.Addr
	configuredDNS                   []string
}

// The verified adapter is the only resource Xray may configure or flush.
// Read-only checks compensate for upstream Close ignoring native flush errors.
type checkedNativeTun struct {
	tun.Tun
	luid           winipcfg.LUID
	guid           string
	ownedRoutes    []netip.Prefix
	ownedAddresses []netip.Addr
	checkIdentity  func(allowMissing bool) error
	snapshot       func(winipcfg.LUID, string) (nativeCleanupSnapshot, error)
	once           sync.Once
	err            error
}

func (t *checkedNativeTun) validateIdentity(allowMissing bool) error {
	if t.checkIdentity != nil {
		return t.checkIdentity(allowMissing)
	}
	if t.luid == 0 {
		index, err := t.Tun.Index()
		if err != nil {
			return errors.New("TUN_LUID_UNAVAILABLE")
		}
		t.luid, err = nativeLUIDFromIndex(index)
		if err != nil {
			return err
		}
	}
	row, err := t.luid.Interface()
	if allowMissing && (errors.Is(err, windows.ERROR_NOT_FOUND) || errors.Is(err, windows.ERROR_FILE_NOT_FOUND)) {
		return nil
	}
	if err != nil {
		return errors.New("TUN_ADAPTER_IDENTITY_UNAVAILABLE")
	}
	guid, err := t.luid.GUID()
	if err != nil || row.Alias() != InterfaceName || !isOwnAdapterGUID(guid.String()) || !strings.EqualFold(guid.String(), t.guid) {
		return errors.New("TUN_ADAPTER_IDENTITY_MISMATCH")
	}
	devices, err := nativeAdapterIdentities()
	if err != nil {
		return errors.New("TUN_ADAPTER_IDENTITY_UNAVAILABLE")
	}
	selected, err := selectNativeAdapter(devices)
	if err != nil {
		return err
	}
	if selected == nil || !strings.EqualFold(selected.guid, t.guid) {
		return errors.New("TUN_ADAPTER_IDENTITY_MISMATCH")
	}
	return nil
}

func (t *checkedNativeTun) Start() error {
	if err := t.validateIdentity(false); err != nil {
		return err
	}
	return t.Tun.Start()
}

func (t *checkedNativeTun) Close() error {
	t.once.Do(func() {
		if err := t.validateIdentity(true); err != nil {
			// Xray Close flushes routes even before Start. Unverified adapters
			// must remain unconfirmed instead of being flushed.
			t.err = errors.Join(errors.New("TUN_CLEANUP_OWNERSHIP_UNCONFIRMED"), err)
			return
		}
		t.err = t.Tun.Close()
		inspect := t.snapshot
		if inspect == nil {
			inspect = readNativeCleanup
		}
		// Device and resolver removal are asynchronous on Windows. Keep the
		// host's WFP protection while observing the owned state settle; never
		// repeat a destructive Close or accept a timeout as clean.
		deadline := time.Now().Add(3 * time.Second)
		for {
			state, err := inspect(t.luid, t.guid)
			if err == nil {
				err = t.verifyCleanup(state)
			}
			if err == nil {
				return
			}
			if !time.Now().Before(deadline) {
				t.err = errors.Join(t.err, err)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
	})
	return t.err
}

func (t *checkedNativeTun) verifyCleanup(state nativeCleanupSnapshot) error {
	var result error
	for _, route := range state.routes {
		if route.InterfaceLUID != t.luid {
			continue
		}
		prefix := route.DestinationPrefix.Prefix()
		owned := false
		for _, configured := range t.ownedRoutes {
			owned = owned || prefix == configured
		}
		for _, address := range t.ownedAddresses {
			owned = owned || prefix.Contains(address)
		}
		if !owned && automaticNativeRoute(route) {
			continue
		}
		result = errors.Join(result, errors.New("TUN_ROUTES_REMAIN"))
		break
	}
	for _, row := range state.addresses {
		if row.InterfaceLUID != t.luid {
			continue
		}
		address := row.Address.Addr()
		owned := false
		for _, configured := range t.ownedAddresses {
			owned = owned || configured == address
		}
		if !owned && address.IsLinkLocalUnicast() {
			continue
		}
		result = errors.Join(result, errors.New("TUN_ADDRESSES_REMAIN"))
		break
	}
	dnsRemaining := len(state.dns) != 0 || (state.interfacePresent && !state.dnsEnumerated)
	for _, value := range state.configuredDNS {
		dnsRemaining = dnsRemaining || strings.TrimSpace(value) != ""
	}
	if dnsRemaining {
		result = errors.Join(result, errors.New("TUN_DNS_CLEANUP_UNCONFIRMED"))
	}
	return result
}

func automaticNativeRoute(route winipcfg.MibIPforwardRow2) bool {
	if route.Protocol != winipcfg.RouteProtocolLocal || !route.NextHop.Addr().IsUnspecified() {
		return false
	}
	prefix := route.DestinationPrefix.Prefix()
	if !prefix.IsValid() {
		return false
	}
	address := prefix.Addr()
	if address.Is4() {
		return (address.IsLinkLocalUnicast() && prefix.Bits() >= 16) || (address.IsMulticast() && prefix.Bits() >= 4) || prefix == netip.MustParsePrefix("255.255.255.255/32")
	}
	return (address.IsLinkLocalUnicast() && prefix.Bits() >= 10) || (address.IsMulticast() && prefix.Bits() >= 8)
}

func readNativeCleanup(luid winipcfg.LUID, guid string) (nativeCleanupSnapshot, error) {
	var state nativeCleanupSnapshot
	var err error
	state.routes, err = winipcfg.GetIPForwardTable2(windows.AF_UNSPEC)
	if err != nil {
		return state, errors.New("TUN_ROUTE_CLEANUP_UNCONFIRMED")
	}
	state.addresses, err = winipcfg.GetUnicastIPAddressTable(windows.AF_UNSPEC)
	if err != nil {
		return state, errors.New("TUN_ADDRESS_CLEANUP_UNCONFIRMED")
	}
	_, err = luid.Interface()
	state.interfacePresent = err == nil
	if err != nil && !errors.Is(err, windows.ERROR_NOT_FOUND) && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return state, errors.New("TUN_INTERFACE_CLEANUP_UNCONFIRMED")
	}
	if state.interfacePresent {
		adapters, err := winipcfg.GetAdaptersAddresses(windows.AF_UNSPEC, winipcfg.GAAFlagIncludeAllInterfaces)
		if err != nil {
			return state, errors.New("TUN_DNS_CLEANUP_UNCONFIRMED")
		}
		for _, adapter := range adapters {
			if adapter.LUID != luid {
				continue
			}
			state.dnsEnumerated = true
			for dns := adapter.FirstDNSServerAddress; dns != nil; dns = dns.Next {
				address, ok := netip.AddrFromSlice(dns.Address.IP())
				if !ok {
					return state, errors.New("TUN_DNS_CLEANUP_UNCONFIRMED")
				}
				state.dns = append(state.dns, address.Unmap())
			}
		}
	}
	// SwDeviceClose makes a device absent without deleting persisted settings.
	// Verify both families even when no active interface can be enumerated.
	for _, service := range []string{"Tcpip", "Tcpip6"} {
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+service+`\Parameters\Interfaces\`+guid, registry.QUERY_VALUE)
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			continue
		}
		if err != nil {
			return state, errors.New("TUN_DNS_CLEANUP_UNCONFIRMED")
		}
		for _, name := range []string{"NameServer", "SearchList"} {
			value, _, err := key.GetStringValue(name)
			if err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
				_ = key.Close()
				return state, errors.New("TUN_DNS_CLEANUP_UNCONFIRMED")
			}
			state.configuredDNS = append(state.configuredDNS, value)
		}
		_ = key.Close()
	}
	return state, nil
}
