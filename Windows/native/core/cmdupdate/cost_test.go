package cmdupdate

import (
	"bluegrass/native/core/update"
	"testing"
)

func TestOnlyExplicitUnrestrictedConnectionCostAllowsAutomaticDownload(t *testing.T) {
	for _, tc := range []struct {
		flags uint32
		want  update.NetworkCost
	}{
		{0, update.NetworkUnknown}, {1, update.NetworkUnmetered},
		{2, update.NetworkMetered}, {4, update.NetworkMetered},
		{1 | 2, update.NetworkMetered}, {1 | 0x10000, update.NetworkMetered},
		{1 | 0x20000, update.NetworkMetered}, {1 | 0x40000, update.NetworkMetered},
		{1 | 0x80000, update.NetworkMetered}, {1 | 0x80000000, update.NetworkUnknown},
	} {
		if got := classifyNLMCost(tc.flags); got != tc.want {
			t.Errorf("flags %#x: got %v want %v", tc.flags, got, tc.want)
		}
	}
}
