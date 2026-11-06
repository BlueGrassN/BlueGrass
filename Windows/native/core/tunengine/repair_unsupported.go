//go:build !windows

package tunengine

func RepairAdapter() error { return &AdapterRepairError{Code: "ADAPTER_REPAIR_UNSUPPORTED"} }
