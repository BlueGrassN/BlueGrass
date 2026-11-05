package tunengine

import (
	"errors"
	"testing"
)

type fakeRepair struct {
	devices     []adapterIdentity
	removed     []string
	removeErr   error
	afterRemove []adapterIdentity
}

func (f *fakeRepair) list() ([]adapterIdentity, error) {
	return append([]adapterIdentity(nil), f.devices...), nil
}
func (f *fakeRepair) remove(id string) error {
	f.removed = append(f.removed, id)
	if f.removeErr != nil {
		return f.removeErr
	}
	if f.afterRemove != nil {
		f.devices = f.afterRemove
		return nil
	}
	var retained []adapterIdentity
	for _, d := range f.devices {
		if d.id != id {
			retained = append(retained, d)
		}
	}
	f.devices = retained
	return nil
}

func TestRepairRequiresGUIDNameAndWintunIdentity(t *testing.T) {
	if got := expectedAdapterGUID(); got != "{3a38783f-dfe0-c9c4-7193-5350bc926164}" {
		t.Fatalf("GUID byte order mismatch: %s", got)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*adapterIdentity)
	}{
		{"wrong GUID", func(d *adapterIdentity) { d.guid = "{00000000-0000-0000-0000-000000000000}" }},
		{"wrong name", func(d *adapterIdentity) { d.name = "Other VPN" }},
		{"wrong service", func(d *adapterIdentity) { d.service = "OtherDriver" }},
		{"wrong hardware", func(d *adapterIdentity) { d.hardwareIDs = []string{"Other"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := adapterIdentity{id: "own", guid: expectedAdapterGUID(), name: InterfaceName, service: "Wintun", hardwareIDs: []string{"Wintun"}}
			tc.mutate(&d)
			f := &fakeRepair{devices: []adapterIdentity{d}}
			_ = repairAdapterWith(f)
			if len(f.removed) != 0 {
				t.Fatal("removed insufficiently identified adapter")
			}
		})
	}
	f := &fakeRepair{devices: []adapterIdentity{{id: "own", guid: expectedAdapterGUID(), name: InterfaceName, service: "Wintun", hardwareIDs: []string{"Wintun"}}}}
	if err := repairAdapterWith(f); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 1 || f.removed[0] != "own" {
		t.Fatal("verified adapter not removed exactly once")
	}
	if err := repairAdapterWith(f); err != nil {
		t.Fatal("not idempotent", err)
	}
}

func TestRepairFailsOnAmbiguousIdentityOrRemovalFailure(t *testing.T) {
	d := adapterIdentity{id: "own", guid: expectedAdapterGUID(), name: InterfaceName, service: "Wintun", hardwareIDs: []string{"Wintun"}}
	f := &fakeRepair{devices: []adapterIdentity{d, d}}
	if repairAdapterWith(f) == nil || len(f.removed) != 0 {
		t.Fatal("ambiguous identity must not delete")
	}
	f = &fakeRepair{devices: []adapterIdentity{d}, removeErr: errors.New("denied")}
	var typed *AdapterRepairError
	if err := repairAdapterWith(f); !errors.As(err, &typed) {
		t.Fatal("failure must be typed")
	}
}
