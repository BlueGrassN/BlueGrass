//go:build !darwin

package tunengine

const InterfaceName = "BlueGrass"

func physicalInterfaceName(name string) bool { return validInterface(name) }

func snapshotPhysicalInterface(string) bool { return true }
