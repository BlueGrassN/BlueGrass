//go:build windows && (amd64 || arm64)

package winprotect

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestDirectDeltaKeepsPersistentBlocksAndExistingFlowFilters(t *testing.T) {
	p := policy{Service: "service.exe", UI: "ui.exe", Tunnel: 42, Virtual: defaultVirtualPrefixes()}
	var serial uint32
	added, removed := 0, 0
	add := func(rule) (windows.GUID, error) { serial++; added++; return windows.GUID{Data1: serial}, nil }
	remove := func(windows.GUID) error { removed++; return nil }
	current, err := applyFilterChanges(nil, compilePolicy(p), add, remove)
	if err != nil {
		t.Fatal(err)
	}
	added, removed = 0, 0
	p.Direct = []directEndpoint{{"tcp", netip.MustParseAddrPort("223.5.5.5:443")}}
	next, err := applyFilterChanges(current, compilePolicy(p), add, remove)
	if err != nil {
		t.Fatal(err)
	}
	if added != 1 || removed != 0 {
		t.Fatalf("direct allow replaced unrelated filters: add=%d remove=%d", added, removed)
	}
	for r, id := range current {
		if next[r] != id {
			t.Fatal("unchanged base/filter identity was replaced")
		}
	}
	added, removed = 0, 0
	p.Direct = nil
	restored, err := applyFilterChanges(next, compilePolicy(p), add, remove)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 || removed != 1 || !reflect.DeepEqual(restored, current) {
		t.Fatal("direct release touched base filters")
	}
}

func TestFilterDeltaDoesNotMutateCommittedCacheOnFailure(t *testing.T) {
	old := rule{Kind: "direct", Permit: true, App: "service.exe", Remote: netip.MustParsePrefix("223.5.5.5/32"), RemotePort: 443, Protocol: 6}
	current := map[rule]windows.GUID{old: {Data1: 1}}
	want := old
	want.Protocol = 17
	for _, removeFails := range []bool{false, true} {
		failure := errors.New("fixture")
		next, err := applyFilterChanges(current, []rule{want}, func(rule) (windows.GUID, error) { return windows.GUID{}, failure }, func(windows.GUID) error {
			if removeFails {
				return failure
			}
			return nil
		})
		if !errors.Is(err, failure) || next != nil || len(current) != 1 || current[old].Data1 != 1 {
			t.Fatal("failed delta changed committed cache")
		}
	}
}

func TestFilterDeltaDeduplicatesEquivalentPolicyEntries(t *testing.T) {
	r := rule{Kind: "block", Persistent: true}
	count := 0
	got, err := applyFilterChanges(nil, []rule{r, r}, func(rule) (windows.GUID, error) { count++; return windows.GUID{Data1: 1}, nil }, func(windows.GUID) error { t.Fatal("unexpected delete"); return nil })
	if err != nil || count != 1 || len(got) != 1 {
		t.Fatal("duplicate equivalent filters were added")
	}
}
