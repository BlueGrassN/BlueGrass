//go:build windows && (amd64 || arm64)

package winprotect

import (
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var wfpDLL = windows.NewLazySystemDLL("fwpuclnt.dll")

// Both LocalSystem and an elevated administrator can create these objects.
// Assigning SYSTEM ownership from an administrator token needs an additional
// restore privilege and otherwise fails with ERROR_INVALID_OWNER. BA ownership
// retains the same privileged-only access and never grants the interactive SID.
const firewallSDDL = "O:BAG:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)"

var (
	procOpen           = wfpDLL.NewProc("FwpmEngineOpen0")
	procClose          = wfpDLL.NewProc("FwpmEngineClose0")
	procBegin          = wfpDLL.NewProc("FwpmTransactionBegin0")
	procCommit         = wfpDLL.NewProc("FwpmTransactionCommit0")
	procAbort          = wfpDLL.NewProc("FwpmTransactionAbort0")
	procProviderAdd    = wfpDLL.NewProc("FwpmProviderAdd0")
	procProviderGet    = wfpDLL.NewProc("FwpmProviderGetByKey0")
	procProviderDelete = wfpDLL.NewProc("FwpmProviderDeleteByKey0")
	procSublayerAdd    = wfpDLL.NewProc("FwpmSubLayerAdd0")
	procSublayerGet    = wfpDLL.NewProc("FwpmSubLayerGetByKey0")
	procSublayerDelete = wfpDLL.NewProc("FwpmSubLayerDeleteByKey0")
	procFilterAdd      = wfpDLL.NewProc("FwpmFilterAdd0")
	procFilterDelete   = wfpDLL.NewProc("FwpmFilterDeleteByKey0")
	procEnumCreate     = wfpDLL.NewProc("FwpmFilterCreateEnumHandle0")
	procEnum           = wfpDLL.NewProc("FwpmFilterEnum0")
	procEnumDestroy    = wfpDLL.NewProc("FwpmFilterDestroyEnumHandle0")
	procLayerCreate    = wfpDLL.NewProc("FwpmLayerCreateEnumHandle0")
	procLayerEnum      = wfpDLL.NewProc("FwpmLayerEnum0")
	procLayerDestroy   = wfpDLL.NewProc("FwpmLayerDestroyEnumHandle0")
	procAppID          = wfpDLL.NewProc("FwpmGetAppIdFromFileName0")
	procFree           = wfpDLL.NewProc("FwpmFreeMemory0")
)

//go:uintptrescapes
func wfpCall(proc *windows.LazyProc, args ...uintptr) error {
	if err := proc.Find(); err != nil {
		return err
	}
	r, _, _ := proc.Call(args...)
	// These APIs return DWORD status, not GetLastError.
	if r != 0 {
		return fmt.Errorf("%s: %w", proc.Name, syscall.Errno(r))
	}
	return nil
}

// FwpmFreeMemory0 returns void, unlike the DWORD-returning WFP operations.
// The allocation always comes from the same DLL, loaded before this call.
func wfpFree[T any](p **T) {
	_, _, _ = procFree.Call(uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
}

type nativeFirewall struct {
	handle  windows.Handle
	sd      *windows.SECURITY_DESCRIPTOR
	apps    map[string]*byteBlob
	current map[rule]windows.GUID // committed filters owned by this exclusive engine
}

func openFirewall() (*nativeFirewall, error) {
	sd, err := windows.SecurityDescriptorFromString(firewallSDDL)
	if err != nil {
		return nil, err
	}
	f := &nativeFirewall{sd: sd, apps: make(map[string]*byteBlob)}
	s := wfpSession{Timeout: 10000} // Flags=0: explicitly NON-dynamic.
	err = wfpCall(procOpen, 0, 10, 0, uintptr(unsafe.Pointer(&s)), uintptr(unsafe.Pointer(&f.handle)))
	if err != nil {
		return nil, err
	}
	return f, nil
}
func (f *nativeFirewall) Close() error {
	if f.handle == 0 {
		return nil
	}
	for k, p := range f.apps {
		wfpFree(&p)
		delete(f.apps, k)
	}
	err := wfpCall(procClose, uintptr(f.handle))
	f.handle = 0
	return err
}
func (f *nativeFirewall) transact(body func() error) error {
	return transaction(func() error { return wfpCall(procBegin, uintptr(f.handle), 0) },
		func() error { return wfpCall(procCommit, uintptr(f.handle)) },
		func() error { return wfpCall(procAbort, uintptr(f.handle)) }, body)
}

func (f *nativeFirewall) objects() (provider, sublayer bool, err error) {
	var p *wfpProvider
	err = wfpCall(procProviderGet, uintptr(f.handle), uintptr(unsafe.Pointer(&providerKey)), uintptr(unsafe.Pointer(&p)))
	if err == nil {
		provider = true
		wfpFree(&p)
	} else if !errors.Is(err, syscall.Errno(0x80320005)) {
		return false, false, err
	}
	var s *wfpSublayer
	err = wfpCall(procSublayerGet, uintptr(f.handle), uintptr(unsafe.Pointer(&sublayerKey)), uintptr(unsafe.Pointer(&s)))
	if err == nil {
		owned := s.Provider != nil && *s.Provider == providerKey
		wfpFree(&s)
		if !owned {
			return false, false, errors.New("WFP sublayer GUID ownership conflict")
		}
		sublayer = true
	} else if !errors.Is(err, syscall.Errno(0x80320007)) {
		return false, false, err
	}
	return provider, sublayer, nil
}
func (f *nativeFirewall) Present() (bool, error) {
	p, s, err := f.objects()
	if err != nil {
		return false, err
	}
	return p || s, nil
}
func (f *nativeFirewall) ensureObjects() error {
	p, s, err := f.objects()
	if err != nil {
		return err
	}
	name, _ := windows.UTF16PtrFromString("BlueGrass persistent network protection")
	if !p {
		o := wfpProvider{Key: providerKey, Display: displayData{Name: name}, Flags: 1}
		// No ServiceName: filters must remain enabled while the service is down.
		if err = wfpCall(procProviderAdd, uintptr(f.handle), uintptr(unsafe.Pointer(&o)), uintptr(unsafe.Pointer(f.sd))); err != nil {
			return err
		}
	}
	if !s {
		o := wfpSublayer{Key: sublayerKey, Display: displayData{Name: name}, Flags: 1, Provider: &providerKey, Weight: 0xffff}
		if err = wfpCall(procSublayerAdd, uintptr(f.handle), uintptr(unsafe.Pointer(&o)), uintptr(unsafe.Pointer(f.sd))); err != nil {
			return err
		}
	}
	return nil
}

func ownsFilter(f *wfpFilter) bool {
	return f != nil && f.Provider != nil && *f.Provider == providerKey && f.Sublayer == sublayerKey
}

func (f *nativeFirewall) ownedFilters() (keys []windows.GUID, err error) {
	layers, err := f.layerKeys()
	if err != nil {
		return nil, err
	}
	for _, layer := range layers {
		batch, err := f.ownedFiltersAt(layer)
		if err != nil {
			return nil, err
		}
		keys = append(keys, batch...)
	}
	return keys, nil
}

// Enumerate actual layers instead of assuming GUID_NULL means all layers for a
// filter template. This also finds this GUID pair's residue on older layers.
func (f *nativeFirewall) layerKeys() (keys []windows.GUID, err error) {
	var h windows.Handle
	if err = wfpCall(procLayerCreate, uintptr(f.handle), 0, uintptr(unsafe.Pointer(&h))); err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, wfpCall(procLayerDestroy, uintptr(f.handle), uintptr(h))) }()
	for {
		var entries **wfpLayer
		var count uint32
		if err = wfpCall(procLayerEnum, uintptr(f.handle), uintptr(h), 256, uintptr(unsafe.Pointer(&entries)), uintptr(unsafe.Pointer(&count))); err != nil {
			return nil, err
		}
		if count > 256 || (count != 0 && entries == nil) {
			if entries != nil {
				wfpFree(&entries)
			}
			return nil, errors.New("invalid WFP layer enumeration response")
		}
		for _, layer := range unsafe.Slice(entries, int(count)) {
			if layer == nil {
				wfpFree(&entries)
				return nil, errors.New("nil WFP layer")
			}
			keys = append(keys, layer.Key)
		}
		if entries != nil {
			wfpFree(&entries)
		}
		if count == 0 {
			break
		}
	}
	return keys, nil
}

func (f *nativeFirewall) ownedFiltersAt(layer windows.GUID) (keys []windows.GUID, err error) {
	// Include BOOTTIME and DISABLED explicitly, otherwise repair leaves residue.
	t := wfpEnumTemplate{Provider: &providerKey, Layer: layer, EnumType: 1, Flags: 0x8 | 0x10, ActionMask: 0xffffffff}
	var h windows.Handle
	if err = wfpCall(procEnumCreate, uintptr(f.handle), uintptr(unsafe.Pointer(&t)), uintptr(unsafe.Pointer(&h))); err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, wfpCall(procEnumDestroy, uintptr(f.handle), uintptr(h))) }()
	for {
		var entries **wfpFilter
		var count uint32
		if err = wfpCall(procEnum, uintptr(f.handle), uintptr(h), 256, uintptr(unsafe.Pointer(&entries)), uintptr(unsafe.Pointer(&count))); err != nil {
			return nil, err
		}
		if count > 256 || (count != 0 && entries == nil) {
			if entries != nil {
				wfpFree(&entries)
			}
			return nil, errors.New("invalid WFP enumeration response")
		}
		for _, v := range unsafe.Slice(entries, int(count)) {
			if ownsFilter(v) {
				keys = append(keys, v.Key)
			}
		}
		if entries != nil {
			wfpFree(&entries)
		}
		if count == 0 {
			break
		}
	}
	return keys, nil
}
func (f *nativeFirewall) deleteFilters() error {
	keys, err := f.ownedFilters()
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err = wfpCall(procFilterDelete, uintptr(f.handle), uintptr(unsafe.Pointer(&key))); err != nil {
			return err
		}
	}
	return nil
}
func (f *nativeFirewall) Replace(rules []rule) error {
	// Resolve executable identities before beginning a transaction; never broaden
	// a missing identity to a process-agnostic permit.
	for _, r := range rules {
		if r.App != "" {
			if _, err := f.appID(r.App); err != nil {
				return err
			}
		}
	}
	var next map[rule]windows.GUID
	err := f.transact(func() error {
		provider, sublayer, err := f.objects()
		if err != nil {
			return err
		}
		if err := f.ensureObjects(); err != nil {
			return err
		}
		current := f.current
		if !provider || !sublayer {
			current = nil
		}
		if current == nil {
			// A fresh engine or an uncertain transaction must reconcile every
			// owned layer, including historical/boot filters, before using a cache.
			if err := f.deleteFilters(); err != nil {
				return err
			}
		}
		next, err = applyFilterChanges(current, rules, f.add, func(key windows.GUID) error {
			return wfpCall(procFilterDelete, uintptr(f.handle), uintptr(unsafe.Pointer(&key)))
		})
		return err
	})
	if err != nil {
		f.current = nil
		return err
	}
	f.current = next
	return nil
}
func (f *nativeFirewall) Remove() error {
	f.current = nil
	return f.transact(func() error {
		p, s, err := f.objects()
		if err != nil {
			return err
		}
		if p || s {
			if err = f.deleteFilters(); err != nil {
				return err
			}
		}
		if s {
			if err = wfpCall(procSublayerDelete, uintptr(f.handle), uintptr(unsafe.Pointer(&sublayerKey))); err != nil {
				return err
			}
		}
		if p {
			if err = wfpCall(procProviderDelete, uintptr(f.handle), uintptr(unsafe.Pointer(&providerKey))); err != nil {
				return err
			}
		}
		return nil
	})
}
func (f *nativeFirewall) appID(path string) (*byteBlob, error) {
	if v := f.apps[path]; v != nil {
		return v, nil
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var blob *byteBlob
	if err = wfpCall(procAppID, uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&blob))); err != nil {
		return nil, err
	}
	if blob == nil || blob.Size == 0 || blob.Data == nil {
		if blob != nil {
			wfpFree(&blob)
		}
		return nil, errors.New("empty WFP application identity")
	}
	f.apps[path] = blob
	return blob, nil
}

// encodedFilter retains pointer-valued union members until FwpmFilterAdd0
// returns. The union itself stores uintptr, which is not a Go GC root.
type encodedFilter struct {
	filter     wfpFilter
	conditions []wfpCondition
	keep       []any
	weight     uint64
}

func encodeFilter(r rule, app *byteBlob) (*encodedFilter, error) {
	e := &encodedFilter{weight: uint64(r.Weight)}
	name, err := windows.UTF16PtrFromString("BlueGrass " + r.Kind)
	if err != nil {
		return nil, err
	}
	e.keep = append(e.keep, name)
	e.filter = wfpFilter{Display: displayData{Name: name}, Provider: &providerKey, Sublayer: sublayerKey, Action: wfpAction{Type: 0x1001}}
	if r.Permit {
		e.filter.Action.Type = 0x1002
	}
	if r.Boot {
		if r.Persistent {
			return nil, errors.New("boot and persistent flags are mutually exclusive")
		}
		e.filter.Flags = 2
	} else if r.Persistent {
		e.filter.Flags = 1
	}
	if r.Boot {
		if r.Inbound {
			e.filter.Layer = packetIn4
			if r.IPv6 {
				e.filter.Layer = packetIn6
			}
		} else {
			e.filter.Layer = packetOut4
			if r.IPv6 {
				e.filter.Layer = packetOut6
			}
		}
	} else {
		if r.Inbound {
			e.filter.Layer = aleAccept4
			if r.IPv6 {
				e.filter.Layer = aleAccept6
			}
		} else {
			e.filter.Layer = aleConnect4
			if r.IPv6 {
				e.filter.Layer = aleConnect6
			}
		}
	}
	e.filter.Weight = wfpValue{Type: 4, Value: uintptr(unsafe.Pointer(&e.weight))}
	add := func(field windows.GUID, match, typ uint32, value uintptr) {
		e.conditions = append(e.conditions, wfpCondition{Field: field, Match: match, Value: wfpValue{Type: typ, Value: value}})
	}
	if r.App != "" {
		if app == nil || app.Size == 0 {
			return nil, errors.New("missing application identity")
		}
		e.keep = append(e.keep, app)
		add(fieldApp, 0, 12, uintptr(unsafe.Pointer(app)))
	}
	if r.Protocol != 0 {
		add(fieldProtocol, 0, 1, uintptr(r.Protocol))
	}
	if r.RemotePort != 0 {
		add(fieldRemotePort, 0, 2, uintptr(r.RemotePort))
	}
	if r.LocalPort != 0 {
		add(fieldLocalPort, 0, 2, uintptr(r.LocalPort))
	}
	if r.NoDNS {
		add(fieldRemotePort, 10, 2, 53)
		add(fieldLocalPort, 10, 2, 53)
	}
	if r.Loopback {
		add(fieldFlags, 6, 3, 1)
	}
	if r.LUID != 0 {
		luid := new(uint64)
		*luid = r.LUID
		e.keep = append(e.keep, luid)
		add(fieldInterface, 0, 4, uintptr(unsafe.Pointer(luid)))
	}
	if r.Remote.IsValid() {
		p := r.Remote.Masked()
		if p.Addr().Is4() {
			a := p.Addr().As4()
			v := &v4Mask{Addr: binary.BigEndian.Uint32(a[:]), Mask: ^uint32(0) << uint(32-p.Bits())}
			e.keep = append(e.keep, v)
			add(fieldRemote, 0, 256, uintptr(unsafe.Pointer(v)))
		} else {
			v := &v6Mask{Addr: p.Addr().As16(), Prefix: uint8(p.Bits())}
			e.keep = append(e.keep, v)
			add(fieldRemote, 0, 257, uintptr(unsafe.Pointer(v)))
		}
	}
	if len(e.conditions) > 0 {
		e.filter.NumConditions = uint32(len(e.conditions))
		e.filter.Conditions = &e.conditions[0]
	}
	return e, nil
}
func (f *nativeFirewall) add(r rule) (windows.GUID, error) {
	e, err := encodeFilter(r, f.apps[r.App])
	if err != nil {
		return windows.GUID{}, err
	}
	e.filter.Key, err = windows.GenerateGUID()
	if err != nil {
		return windows.GUID{}, err
	}
	err = wfpCall(procFilterAdd, uintptr(f.handle), uintptr(unsafe.Pointer(&e.filter)), uintptr(unsafe.Pointer(f.sd)), 0)
	runtime.KeepAlive(e)
	return e.filter.Key, err
}

// Runs inside the caller's WFP transaction. Unchanged filters keep their IDs,
// avoiding deletion/recreation of unrelated policy for each direct socket.
// Windows may still reauthorize affected flows after a filter change.
// Never mutate the committed cache until both this body and commit succeed.
func applyFilterChanges(current map[rule]windows.GUID, rules []rule, add func(rule) (windows.GUID, error), remove func(windows.GUID) error) (map[rule]windows.GUID, error) {
	wanted := make(map[rule]bool, len(rules))
	for _, r := range rules {
		wanted[r] = true
	}
	for r, key := range current {
		if !wanted[r] {
			if err := remove(key); err != nil {
				return nil, err
			}
		}
	}
	next := make(map[rule]windows.GUID, len(wanted))
	for _, r := range rules {
		if _, exists := next[r]; exists {
			continue
		}
		if key, exists := current[r]; exists {
			next[r] = key
			continue
		}
		key, err := add(r)
		if err != nil {
			return nil, err
		}
		next[r] = key
	}
	return next, nil
}
