//go:build windows

package cmdupdate

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var errNLM = errors.New("UPDATE_NETWORK_COST_UNKNOWN")

var (
	ole32                    = windows.NewLazySystemDLL("ole32.dll")
	coInitializeEx           = ole32.NewProc("CoInitializeEx")
	coUninitialize           = ole32.NewProc("CoUninitialize")
	coCreateInstance         = ole32.NewProc("CoCreateInstance")
	clsidNetworkListManager  = windows.GUID{Data1: 0xDCB00C01, Data2: 0x570F, Data3: 0x4A9B, Data4: [8]byte{0x8D, 0x69, 0x19, 0x9F, 0xDB, 0xA5, 0x72, 0x3B}}
	iidNetworkListManager    = windows.GUID{Data1: 0xDCB00000, Data2: 0x570F, Data3: 0x4A9B, Data4: [8]byte{0x8D, 0x69, 0x19, 0x9F, 0xDB, 0xA5, 0x72, 0x3B}}
	iidNetworkConnectionCost = windows.GUID{Data1: 0xDCB0000A, Data2: 0x570F, Data3: 0x4A9B, Data4: [8]byte{0x8D, 0x69, 0x19, 0x9F, 0xDB, 0xA5, 0x72, 0x3B}}
)

type nlmCOM struct{ vtable *uintptr }

//go:uintptrescapes
func (o *nlmCOM) call(slot int, args ...uintptr) uint32 {
	method := unsafe.Slice(o.vtable, slot+1)[slot]
	arguments := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	result, _, _ := syscall.SyscallN(method, arguments...)
	runtime.KeepAlive(o)
	return uint32(result)
}
func (o *nlmCOM) release() {
	if o != nil {
		o.call(2)
	}
}
func failedHRESULT(code uint32) bool { return int32(code) < 0 }

type nlmResult struct {
	cost connectionCost
	err  error
}
type nlmRequest struct {
	ctx     context.Context
	adapter windows.GUID
	result  chan nlmResult
}
type nlmReader struct {
	requests chan nlmRequest
	stop     chan struct{}
	once     sync.Once
}

func newNLMReader() *nlmReader {
	r := &nlmReader{requests: make(chan nlmRequest), stop: make(chan struct{})}
	go r.run()
	return r
}
func (r *nlmReader) Close() { r.once.Do(func() { close(r.stop) }) }
func (r *nlmReader) Read(ctx context.Context, adapterGUID string) (connectionCost, error) {
	guid, err := windows.GUIDFromString(adapterGUID)
	if err != nil || guid == (windows.GUID{}) {
		return connectionCost{}, errNLM
	}
	ctx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
	defer cancel()
	request := nlmRequest{ctx: ctx, adapter: guid, result: make(chan nlmResult, 1)}
	select {
	case <-ctx.Done():
		return connectionCost{}, errNLM
	case <-r.stop:
		return connectionCost{}, errNLM
	case r.requests <- request:
	}
	select {
	case <-ctx.Done():
		return connectionCost{}, errNLM
	case <-r.stop:
		return connectionCost{}, errNLM
	case result := <-request.result:
		if ctx.Err() != nil {
			return connectionCost{}, errNLM
		}
		return result.cost, result.err
	}
}
func (r *nlmReader) run() {
	// All COM pointers stay on this apartment/thread. A blocked OS COM call
	// cannot delay the caller past Read's deadline or spawn unbounded workers.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	status, _, _ := coInitializeEx.Call(0, 0) // COINIT_MULTITHREADED
	initialized := !failedHRESULT(uint32(status))
	if initialized {
		defer coUninitialize.Call()
	}
	var manager *nlmCOM
	if initialized {
		status, _, _ = coCreateInstance.Call(uintptr(unsafe.Pointer(&clsidNetworkListManager)), 0, 5, uintptr(unsafe.Pointer(&iidNetworkListManager)), uintptr(unsafe.Pointer(&manager)))
		if failedHRESULT(uint32(status)) {
			manager = nil
		}
	}
	defer manager.release()
	for {
		select {
		case <-r.stop:
			return
		case request := <-r.requests:
			result := nlmResult{err: errNLM}
			if manager != nil && request.ctx.Err() == nil {
				result.cost, result.err = readNLMConnection(manager, request.adapter)
			}
			request.result <- result
		}
	}
}

func readNLMConnection(manager *nlmCOM, adapter windows.GUID) (connectionCost, error) {
	var enumerator *nlmCOM
	// netlistmgr.h: IDispatch's seven slots, GetNetworks, GetNetwork,
	// GetNetworkConnections. Never query the machine-wide default cost.
	if failedHRESULT(manager.call(9, uintptr(unsafe.Pointer(&enumerator)))) || enumerator == nil {
		return connectionCost{}, errNLM
	}
	defer enumerator.release()
	var result connectionCost
	matched := false
	for count := 0; count < 256; count++ {
		var connection *nlmCOM
		var fetched uint32
		status := enumerator.call(8, 1, uintptr(unsafe.Pointer(&connection)), uintptr(unsafe.Pointer(&fetched))) // Next after IDispatch/get__NewEnum
		if status == 1 && fetched == 0 {
			if matched {
				return result, nil
			}
			return connectionCost{}, errNLM
		}
		if status != 0 || fetched != 1 || connection == nil {
			connection.release()
			return connectionCost{}, errNLM
		}
		value, isMatch, err := readOneConnection(connection, adapter)
		connection.release()
		if err != nil {
			return connectionCost{}, err
		}
		if isMatch {
			if matched {
				return connectionCost{}, errNLM
			} // ambiguous adapter mapping
			result, matched = value, true
		}
	}
	return connectionCost{}, errNLM
}
func readOneConnection(connection *nlmCOM, adapter windows.GUID) (connectionCost, bool, error) {
	var found windows.GUID
	if failedHRESULT(connection.call(12, uintptr(unsafe.Pointer(&found)))) {
		return connectionCost{}, false, errNLM
	}
	if found != adapter {
		return connectionCost{}, false, nil
	}
	var connected int16
	if failedHRESULT(connection.call(9, uintptr(unsafe.Pointer(&connected)))) || connected != -1 {
		return connectionCost{}, false, errNLM
	}
	var connectionID windows.GUID
	if failedHRESULT(connection.call(11, uintptr(unsafe.Pointer(&connectionID)))) || connectionID == (windows.GUID{}) {
		return connectionCost{}, false, errNLM
	}
	var network *nlmCOM
	if failedHRESULT(connection.call(7, uintptr(unsafe.Pointer(&network)))) || network == nil {
		return connectionCost{}, false, errNLM
	}
	defer network.release()
	var networkID windows.GUID
	if failedHRESULT(network.call(11, uintptr(unsafe.Pointer(&networkID)))) || networkID == (windows.GUID{}) {
		return connectionCost{}, false, errNLM
	}
	var cost *nlmCOM
	if failedHRESULT(connection.call(0, uintptr(unsafe.Pointer(&iidNetworkConnectionCost)), uintptr(unsafe.Pointer(&cost)))) || cost == nil {
		return connectionCost{}, false, errNLM
	}
	defer cost.release()
	var flags uint32
	if failedHRESULT(cost.call(3, uintptr(unsafe.Pointer(&flags)))) {
		return connectionCost{}, false, errNLM
	}
	return connectionCost{AdapterGUID: strings.ToLower(found.String()), ConnectionGUID: strings.ToLower(connectionID.String()), NetworkGUID: strings.ToLower(networkID.String()), Flags: flags}, true, nil
}
