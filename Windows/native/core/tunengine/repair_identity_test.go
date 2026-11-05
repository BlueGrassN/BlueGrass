package tunengine

import (
	"errors"
	"strings"
	"testing"
)

// These fixed values pin Windows GUID byte order independently of the repair
// implementation: MD5("BlueGrass") and MD5("wintunBlueGrass"), respectively.
var repairGUIDFixtures = []struct{ name, guid string }{
	{"xray", "{3a38783f-dfe0-c9c4-7193-5350bc926164}"},
	{"legacy", "{a72ced18-5879-4bd0-6e9c-344e1e99d94f}"},
}

func repairTestAdapter(id, guid string) adapterIdentity {
	return adapterIdentity{id: id, guid: guid, name: "BlueGrass", service: "Wintun", hardwareIDs: []string{"Wintun"}}
}

func requireRepairError(t *testing.T, err error, code string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatalf("unexpected repair error: %v", err)
		}
		return
	}
	var typed *AdapterRepairError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("repair error = %v, want %s", err, code)
	}
}

func TestLegacyAdapterGUIDMatchesUpgradedAdapter(t *testing.T) {
	if got := legacyAdapterGUID(); got != repairGUIDFixtures[1].guid {
		t.Fatalf("legacy GUID byte order mismatch: %s", got)
	}
}

func TestIsOwnAdapterGUID(t *testing.T) {
	for _, identity := range repairGUIDFixtures {
		t.Run(identity.name, func(t *testing.T) {
			for _, guid := range []string{identity.guid, strings.ToUpper(identity.guid)} {
				if !isOwnAdapterGUID(guid) {
					t.Fatalf("product GUID rejected: %q", guid)
				}
			}
			for _, guid := range []string{
				strings.Trim(identity.guid, "{}"),
				" " + identity.guid,
				identity.guid + " ",
				identity.guid + "suffix",
			} {
				if isOwnAdapterGUID(guid) {
					t.Fatalf("malformed GUID accepted: %q", guid)
				}
			}
		})
	}
	for _, guid := range []string{
		"", "BlueGrass", "{00000000-0000-0000-0000-000000000000}",
		"{3f78383a-e0df-c4c9-7193-5350bc926164}", // MD5 bytes in network order.
	} {
		if isOwnAdapterGUID(guid) {
			t.Fatalf("unrelated GUID accepted: %q", guid)
		}
	}
}

func TestRepairRecognizesBothProductGUIDs(t *testing.T) {
	for _, identity := range repairGUIDFixtures {
		t.Run(identity.name, func(t *testing.T) {
			d := repairTestAdapter("own", strings.ToUpper(identity.guid))
			d.service = "wInTuN"
			d.hardwareIDs = []string{"Other", "WINTUN"}
			if !isOwnAdapter(d) {
				t.Fatal("valid product adapter not recognized")
			}
			unrelated := repairTestAdapter("unrelated", "{00000000-0000-0000-0000-000000000000}")
			f := &fakeRepair{devices: []adapterIdentity{unrelated, d}}
			requireRepairError(t, repairAdapterWith(f), "")
			if len(f.removed) != 1 || f.removed[0] != "own" {
				t.Fatalf("removed = %v, want only own", f.removed)
			}
			if len(f.devices) != 1 || f.devices[0].id != "unrelated" {
				t.Fatal("unrelated same-name Wintun adapter changed")
			}
			requireRepairError(t, repairAdapterWith(f), "")
			if len(f.removed) != 1 {
				t.Fatal("repair was not idempotent")
			}
		})
	}
}

func TestRepairBothGUIDsRequireExactAliasAndWintun(t *testing.T) {
	for _, identity := range repairGUIDFixtures {
		for _, tc := range []struct {
			name   string
			mutate func(*adapterIdentity)
			code   string
		}{
			{"wrong GUID", func(d *adapterIdentity) { d.guid = "{00000000-0000-0000-0000-000000000000}" }, ""},
			{"missing GUID", func(d *adapterIdentity) { d.guid = "" }, ""},
			{"wrong name", func(d *adapterIdentity) { d.name = "Other VPN" }, "ADAPTER_IDENTITY_MISMATCH"},
			{"name case differs", func(d *adapterIdentity) { d.name = "bluegrass" }, "ADAPTER_IDENTITY_MISMATCH"},
			{"name whitespace", func(d *adapterIdentity) { d.name = "BlueGrass " }, "ADAPTER_IDENTITY_MISMATCH"},
			{"name suffix", func(d *adapterIdentity) { d.name = "BlueGrass 2" }, "ADAPTER_IDENTITY_MISMATCH"},
			{"missing name", func(d *adapterIdentity) { d.name = "" }, "ADAPTER_IDENTITY_MISMATCH"},
			{"wrong service", func(d *adapterIdentity) { d.service = "OtherDriver" }, "ADAPTER_IDENTITY_MISMATCH"},
			{"missing service", func(d *adapterIdentity) { d.service = "" }, "ADAPTER_IDENTITY_MISMATCH"},
			{"wrong hardware", func(d *adapterIdentity) { d.hardwareIDs = []string{"Other"} }, "ADAPTER_IDENTITY_MISMATCH"},
			{"hardware prefix", func(d *adapterIdentity) { d.hardwareIDs = []string{"WintunOther"} }, "ADAPTER_IDENTITY_MISMATCH"},
			{"missing hardware", func(d *adapterIdentity) { d.hardwareIDs = nil }, "ADAPTER_IDENTITY_MISMATCH"},
		} {
			t.Run(identity.name+"/"+tc.name, func(t *testing.T) {
				d := repairTestAdapter("own", identity.guid)
				tc.mutate(&d)
				if isOwnAdapter(d) {
					t.Fatal("insufficiently identified adapter accepted")
				}
				f := &fakeRepair{devices: []adapterIdentity{d}}
				requireRepairError(t, repairAdapterWith(f), tc.code)
				if len(f.removed) != 0 {
					t.Fatal("removed insufficiently identified adapter")
				}
			})
		}
	}
}

func TestRepairFailsOnAmbiguousProductGUIDs(t *testing.T) {
	for _, first := range repairGUIDFixtures {
		for _, second := range repairGUIDFixtures {
			t.Run(first.name+"/"+second.name, func(t *testing.T) {
				f := &fakeRepair{devices: []adapterIdentity{
					repairTestAdapter("first", first.guid),
					repairTestAdapter("second", second.guid),
				}}
				requireRepairError(t, repairAdapterWith(f), "ADAPTER_IDENTITY_AMBIGUOUS")
				if len(f.removed) != 0 {
					t.Fatal("ambiguous identity must not delete")
				}
			})
		}
	}
}

func TestRepairRejectsConflictingProductIdentityInEitherOrder(t *testing.T) {
	for _, first := range repairGUIDFixtures {
		for _, second := range repairGUIDFixtures {
			t.Run(first.name+"/"+second.name, func(t *testing.T) {
				own := repairTestAdapter("own", first.guid)
				conflict := repairTestAdapter("conflict", second.guid)
				conflict.name = "Other VPN"
				for _, devices := range [][]adapterIdentity{{own, conflict}, {conflict, own}} {
					f := &fakeRepair{devices: devices}
					if err := repairAdapterWith(f); err == nil {
						t.Fatal("conflicting product identity must fail closed")
					}
					if len(f.removed) != 0 {
						t.Fatalf("conflict caused removal: %v", f.removed)
					}
				}
			})
		}
	}
}

func TestRepairPostRemovalChecksBothGUIDs(t *testing.T) {
	for _, initial := range repairGUIDFixtures {
		for _, remaining := range repairGUIDFixtures {
			t.Run(initial.name+"/"+remaining.name, func(t *testing.T) {
				f := &fakeRepair{
					devices:     []adapterIdentity{repairTestAdapter("own", initial.guid)},
					afterRemove: []adapterIdentity{{id: "remaining", guid: strings.ToUpper(remaining.guid)}},
				}
				requireRepairError(t, repairAdapterWith(f), "ADAPTER_REMOVAL_PENDING")
				if len(f.removed) != 1 || f.removed[0] != "own" {
					t.Fatalf("removed = %v, want only own", f.removed)
				}
			})
		}
	}
}

func TestRepairBothGUIDsSurfaceRemovalFailure(t *testing.T) {
	for _, identity := range repairGUIDFixtures {
		t.Run(identity.name, func(t *testing.T) {
			f := &fakeRepair{
				devices:   []adapterIdentity{repairTestAdapter("own", identity.guid)},
				removeErr: errors.New("denied"),
			}
			requireRepairError(t, repairAdapterWith(f), "ADAPTER_REMOVAL_FAILED")
		})
	}
}
