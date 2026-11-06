package cmdupdate

import "bluegrass/native/core/update"

// NLM_CONNECTION_COST from the Windows SDK. Only exact UNRESTRICTED permits
// automatic download; expensive flags dominate it and unknown bits fail closed.
func classifyNLMCost(flags uint32) update.NetworkCost {
	const restrictive = uint32(0x2 | 0x4 | 0x10000 | 0x20000 | 0x40000 | 0x80000)
	if flags & ^uint32(1|restrictive) != 0 {
		return update.NetworkUnknown
	}
	if flags&restrictive != 0 {
		return update.NetworkMetered
	}
	if flags == 1 {
		return update.NetworkUnmetered
	}
	return update.NetworkUnknown
}

type connectionCost struct {
	AdapterGUID, ConnectionGUID, NetworkGUID string
	Flags                                    uint32
}
