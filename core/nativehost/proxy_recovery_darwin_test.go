//go:build darwin

package nativehost

import (
	"encoding/json"
	"errors"
	"testing"

	"bluegrass/native/core/macproxy"
	"bluegrass/native/core/winproxy"
)

// Replace only native preferences/Keychain; execute the real Darwin manager and
// nativehost Disconnect/Close/repair paths without touching macOS settings.
type darwinRecoveryMemory struct {
	snapshot macproxy.Snapshot
	record   *macproxy.Record
}

func recoveryCopy[T any](v T) T {
	data, _ := json.Marshal(v)
	var copy T
	_ = json.Unmarshal(data, &copy)
	return copy
}
func (d *darwinRecoveryMemory) Read() (macproxy.Snapshot, error) {
	return recoveryCopy(d.snapshot), nil
}
func (d *darwinRecoveryMemory) Load() (*macproxy.Record, error) { return recoveryCopy(d.record), nil }
func (d *darwinRecoveryMemory) Save(r macproxy.Record) error    { d.record = recoveryCopy(&r); return nil }
func (d *darwinRecoveryMemory) Delete() error                   { d.record = nil; return nil }
func (d *darwinRecoveryMemory) CAS(_, next macproxy.Snapshot) error {
	d.snapshot = recoveryCopy(next)
	return nil
}

func TestDarwinDisconnectCannotAcknowledgeUnresolvedLoopback(t *testing.T) {
	d := &darwinRecoveryMemory{snapshot: macproxy.Snapshot{Active: []string{"wifi"}, Services: map[string]macproxy.Settings{"wifi": {"HTTPEnable": float64(0)}}}}
	m := macproxy.NewWithDriver(d)
	if err := m.Apply("127.0.0.1:12808", ""); err != nil {
		t.Fatal(err)
	}
	d.snapshot.Services["wifi"]["HTTPEnable"] = false
	h, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	h.proxy = m
	r := h.Handle(Request{ID: "disconnect", Command: "Disconnect"})
	if r.OK || r.Error != "RECOVERY_REQUIRED" || r.Snapshot["proxyRecoveryRequired"] != true || d.record == nil {
		t.Fatal("false cleanup success", r)
	}
	if d.snapshot.Services["wifi"]["HTTPSProxy"] == "127.0.0.1" {
		t.Fatal("independently owned HTTPS was stranded")
	}
	if err := h.Close(); !errors.Is(err, winproxy.ErrRecoveryRequired) || d.record == nil {
		t.Fatal("Close forgot unresolved proxy", err)
	}
	if preserved, err := repairProxy(m); preserved || !errors.Is(err, winproxy.ErrRecoveryRequired) || d.record == nil {
		t.Fatal("repair falsely acknowledged cleanup", preserved, err)
	}
}
