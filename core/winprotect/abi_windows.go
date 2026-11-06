//go:build windows && (amd64 || arm64)

// Windows WFP ABI definitions reference github.com/tailscale/wf (BSD-3-Clause;
// see LICENSE.wf), checked against Windows SDK 10.0.26100.0 fwpmtypes.h,
// fwptypes.h and fwpmu.h. Only the required subset is included.
package winprotect

import "golang.org/x/sys/windows"

const (
	ProviderGUID = "{ec26f58b-7149-40ea-8d68-17be93af3bd1}"
	SublayerGUID = "{e0da41ca-0718-41bd-96ad-cbe277ad6a0f}"
)

var (
	providerKey     = windows.GUID{Data1: 0xec26f58b, Data2: 0x7149, Data3: 0x40ea, Data4: [8]byte{0x8d, 0x68, 0x17, 0xbe, 0x93, 0xaf, 0x3b, 0xd1}}
	sublayerKey     = windows.GUID{Data1: 0xe0da41ca, Data2: 0x0718, Data3: 0x41bd, Data4: [8]byte{0x96, 0xad, 0xcb, 0xe2, 0x77, 0xad, 0x6a, 0x0f}}
	aleConnect4     = windows.GUID{Data1: 0xc38d57d1, Data2: 0x05a7, Data3: 0x4c33, Data4: [8]byte{0x90, 0x4f, 0x7f, 0xbc, 0xee, 0xe6, 0x0e, 0x82}}
	aleConnect6     = windows.GUID{Data1: 0x4a72393b, Data2: 0x319f, Data3: 0x44bc, Data4: [8]byte{0x84, 0xc3, 0xba, 0x54, 0xdc, 0xb3, 0xb6, 0xb4}}
	aleAccept4      = windows.GUID{Data1: 0xe1cd9fe7, Data2: 0xf4b5, Data3: 0x4273, Data4: [8]byte{0x96, 0xc0, 0x59, 0x2e, 0x48, 0x7b, 0x86, 0x50}}
	aleAccept6      = windows.GUID{Data1: 0xa3b42c97, Data2: 0x9f04, Data3: 0x4672, Data4: [8]byte{0xb8, 0x7e, 0xce, 0xe9, 0xc4, 0x83, 0x25, 0x7f}}
	packetIn4       = windows.GUID{Data1: 0xc86fd1bf, Data2: 0x21cd, Data3: 0x497e, Data4: [8]byte{0xa0, 0xbb, 0x17, 0x42, 0x5c, 0x88, 0x5c, 0x58}}
	packetIn6       = windows.GUID{Data1: 0xf52032cb, Data2: 0x991c, Data3: 0x46e7, Data4: [8]byte{0x97, 0x1d, 0x26, 0x01, 0x45, 0x9a, 0x91, 0xca}}
	packetOut4      = windows.GUID{Data1: 0x1e5c9fae, Data2: 0x8a84, Data3: 0x4135, Data4: [8]byte{0xa3, 0x31, 0x95, 0x0b, 0x54, 0x22, 0x9e, 0xcd}}
	packetOut6      = windows.GUID{Data1: 0xa3b3ab6b, Data2: 0x3564, Data3: 0x488c, Data4: [8]byte{0x91, 0x17, 0xf3, 0x4e, 0x82, 0x14, 0x27, 0x63}}
	fieldApp        = windows.GUID{Data1: 0xd78e1e87, Data2: 0x8644, Data3: 0x4ea5, Data4: [8]byte{0x94, 0x37, 0xd8, 0x09, 0xec, 0xef, 0xc9, 0x71}}
	fieldFlags      = windows.GUID{Data1: 0x632ce23b, Data2: 0x5167, Data3: 0x435c, Data4: [8]byte{0x86, 0xd7, 0xe9, 0x03, 0x68, 0x4a, 0xa8, 0x0c}}
	fieldInterface  = windows.GUID{Data1: 0x4cd62a49, Data2: 0x59c3, Data3: 0x4969, Data4: [8]byte{0xb7, 0xf3, 0xbd, 0xa5, 0xd3, 0x28, 0x90, 0xa4}}
	fieldLocalPort  = windows.GUID{Data1: 0x0c1ba1af, Data2: 0x5765, Data3: 0x453f, Data4: [8]byte{0xaf, 0x22, 0xa8, 0xf7, 0x91, 0xac, 0x77, 0x5b}}
	fieldProtocol   = windows.GUID{Data1: 0x3971ef2b, Data2: 0x623e, Data3: 0x4f9a, Data4: [8]byte{0x8c, 0xb1, 0x6e, 0x79, 0xb8, 0x06, 0xb9, 0xa7}}
	fieldRemote     = windows.GUID{Data1: 0xb235ae9a, Data2: 0x1d64, Data3: 0x49b8, Data4: [8]byte{0xa4, 0x4c, 0x5f, 0xf3, 0xd9, 0x09, 0x50, 0x45}}
	fieldRemotePort = windows.GUID{Data1: 0xc35a604d, Data2: 0xd22b, Data3: 0x4e1a, Data4: [8]byte{0x91, 0xb4, 0x68, 0xf6, 0x74, 0xee, 0x67, 0x4b}}
)

type displayData struct{ Name, Description *uint16 }
type byteBlob struct {
	Size uint32
	Data *byte
}
type wfpValue struct {
	Type  uint32
	Value uintptr
}
type wfpSession struct {
	Key                       windows.GUID
	Display                   displayData
	Flags, Timeout, ProcessID uint32
	SID                       *windows.SID
	Username                  *uint16
	KernelMode                int32
}
type wfpProvider struct {
	Key     windows.GUID
	Display displayData
	Flags   uint32
	Data    byteBlob
	Service *uint16
}
type wfpSublayer struct {
	Key      windows.GUID
	Display  displayData
	Flags    uint32
	Provider *windows.GUID
	Data     byteBlob
	Weight   uint16
}
type wfpAction struct {
	Type uint32
	Key  windows.GUID
}
type wfpCondition struct {
	Field windows.GUID
	Match uint32
	Value wfpValue
}
type wfpFilter struct {
	Key             windows.GUID
	Display         displayData
	Flags           uint32
	Provider        *windows.GUID
	Data            byteBlob
	Layer, Sublayer windows.GUID
	Weight          wfpValue
	NumConditions   uint32
	Conditions      *wfpCondition
	Action          wfpAction
	// The C anonymous union is 16 bytes, aligned to UINT64, not two fields.
	Context         [2]uint64
	Reserved        *windows.GUID
	ID              uint64
	EffectiveWeight wfpValue
}
type wfpEnumTemplate struct {
	Provider        *windows.GUID
	Layer           windows.GUID
	EnumType, Flags uint32
	ProviderContext uintptr
	NumConditions   uint32
	Conditions      *wfpCondition
	ActionMask      uint32
	Callout         *windows.GUID
}
type wfpLayer struct {
	Key              windows.GUID
	Display          displayData
	Flags, NumFields uint32
	Fields           uintptr // Opaque FWPM_FIELD0 array, never dereferenced here.
	DefaultSublayer  windows.GUID
	ID               uint16
}
type v4Mask struct{ Addr, Mask uint32 }
type v6Mask struct {
	Addr   [16]byte
	Prefix uint8
}
