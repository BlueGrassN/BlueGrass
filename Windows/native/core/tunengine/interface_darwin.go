//go:build darwin

package tunengine

const InterfaceName = "utun233"

func physicalInterfaceName(name string) bool { return darwinPhysicalName(name) }

func snapshotPhysicalInterface(name string) bool { return darwinPhysicalName(name) }
