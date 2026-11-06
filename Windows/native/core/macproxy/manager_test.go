package macproxy

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"bluegrass/native/core/winproxy"
)

// This fake substitutes only privileged OS storage and CAS; the transaction,
// ownership, and recovery decisions are the production Manager.
type memoryDriver struct {
	s                                Snapshot
	r                                *Record
	failSave, failCAS, partial, race bool
	failRollback                     bool
}

func TestRestorePreservesExternalEndpointAsCoherentGroup(t *testing.T) {
	for _, prefix := range []string{"HTTP", "HTTPS", "SOCKS"} {
		t.Run(prefix, func(t *testing.T) {
			m, d := fixture()
			d.s.Services["wifi"][prefix+"Enable"] = float64(0)
			if prefix == "SOCKS" {
				d.s.Services["wifi"][prefix+"Enable"] = float64(1)
			}
			d.s.Services["wifi"][prefix+"Proxy"] = "old.example"
			d.s.Services["wifi"][prefix+"Port"] = float64(9000)
			if err := m.Apply("127.0.0.1:12808", ""); err != nil {
				t.Fatal(err)
			}
			d.s.Services["wifi"][prefix+"Proxy"] = "external.example"
			d.s.Services["wifi"][prefix+"Port"] = float64(8080)
			external := clone(d.s.Services["wifi"])
			if err := m.Restore(); !errors.Is(err, winproxy.ErrOwnershipLost) {
				t.Fatal(err)
			}
			for _, suffix := range []string{"Enable", "Proxy", "Port"} {
				k := prefix + suffix
				if !equal(d.s.Services["wifi"][k], external[k]) {
					t.Fatalf("changed competing %s", k)
				}
			}
			if d.s.Services["wired"]["HTTPEnable"] != float64(0) {
				t.Fatal("other owned service was not restored")
			}
		})
	}
}
func TestRestorePreservesPACEnableWhenExternalURLChanges(t *testing.T) {
	m, d := fixture()
	if err := m.Apply("127.0.0.1:12808", ""); err != nil {
		t.Fatal(err)
	}
	d.s.Services["wifi"]["ProxyAutoConfigURLString"] = "https://external/pac"
	if err := m.Restore(); !errors.Is(err, winproxy.ErrOwnershipLost) {
		t.Fatal(err)
	}
	if !equal(d.s.Services["wifi"]["ProxyAutoConfigEnable"], 0) || d.s.Services["wifi"]["ProxyAutoConfigURLString"] != "https://external/pac" {
		t.Fatal("changed external PAC behavior")
	}
}
func TestManagerMutexWaitSharesTransactionDeadline(t *testing.T) {
	m, _ := fixture()
	end, err := m.beginOperation(200 * time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	start := time.Now()
	if finish, err := m.beginOperation(30 * time.Millisecond); err == nil {
		finish()
		t.Fatal("entered occupied transaction")
	}
	if time.Since(start) > 150*time.Millisecond {
		t.Fatal("mutex wait exceeded transaction deadline")
	}
}

type authorizationWaitDriver struct {
	*memoryDriver
	entered    chan struct{}
	authorized chan struct{}
}

func (d *authorizationWaitDriver) CAS(a, b Snapshot) error {
	close(d.entered)
	<-d.authorized
	return d.memoryDriver.CAS(a, b)
}
func TestStatusWaitsForAuthorizationWithoutInvalidatingConnectingAttempt(t *testing.T) {
	_, memory := fixture()
	d := &authorizationWaitDriver{memoryDriver: memory, entered: make(chan struct{}), authorized: make(chan struct{})}
	m := NewWithDriver(d)
	apply := make(chan error, 1)
	go func() { apply <- m.Apply("127.0.0.1:12808", "") }()
	<-d.entered
	type status struct {
		active bool
		err    error
	}
	state := make(chan status, 1)
	go func() { active, err := m.State(); state <- status{active, err} }()
	select {
	case result := <-state:
		close(d.authorized)
		<-apply
		t.Fatal("status rejected live authorization wait", result)
	case <-time.After(3100 * time.Millisecond):
	}
	close(d.authorized)
	if err := <-apply; err != nil {
		t.Fatal(err)
	}
	result := <-state
	if !result.active || result.err != nil {
		t.Fatal("status failed after authorization", result)
	}
}

func clone[T any](v T) T                        { b, _ := encode(v); var out T; _ = decode(b, &out); return out }
func (d *memoryDriver) Read() (Snapshot, error) { return clone(d.s), nil }
func (d *memoryDriver) Load() (*Record, error)  { return clone(d.r), nil }
func (d *memoryDriver) Save(r Record) error {
	if d.failRollback && r.Phase == "restoring" {
		return errors.New("rollback journal unavailable")
	}
	if d.failSave && r.Phase == "applied" {
		return errors.New("save")
	}
	d.r = clone(&r)
	return nil
}

func TestFailedRollbackRetainsJournalForLaterRecovery(t *testing.T) {
	m, d := fixture()
	original := clone(d.s)
	d.failCAS = true
	d.partial = true
	d.failRollback = true
	if m.Apply("127.0.0.1:12808", "") == nil || d.r == nil {
		t.Fatal("failed rollback lost recovery record")
	}
	d.failRollback = false
	if err := NewWithDriver(d).Recover(); err != nil {
		t.Fatal(err)
	}
	if d.r != nil || !reflect.DeepEqual(d.s, original) {
		t.Fatal("later recovery did not restore partial apply")
	}
}
func TestCorruptJournalCannotClaimForeignProxy(t *testing.T) {
	m, d := fixture()
	d.r = &Record{Version: 1, Phase: "applied", Active: clone(d.s.Active), Entries: map[string]Change{}}
	for id, s := range d.s.Services {
		d.r.Entries[id] = Change{Original: clone(s), Applied: clone(s)}
	}
	if ok, err := m.State(); ok || !errors.Is(err, winproxy.ErrInvalidRecord) {
		t.Fatal("corrupt journal claimed arbitrary existing settings", ok, err)
	}
}
func (d *memoryDriver) Delete() error { d.r = nil; return nil }
func (d *memoryDriver) CAS(expected, desired Snapshot) error {
	if d.race {
		d.race = false
		d.s.Services["wifi"]["HTTPProxy"] = "competitor"
	}
	if !reflect.DeepEqual(d.s, expected) {
		return winproxy.ErrOwnershipLost
	}
	if d.failCAS {
		d.failCAS = false
		if d.partial {
			d.s.Services["wifi"] = clone(desired.Services["wifi"])
		}
		return errors.New("commit")
	}
	d.s = clone(desired)
	return nil
}
func fixture() (*Manager, *memoryDriver) {
	d := &memoryDriver{s: Snapshot{Active: []string{"wifi", "wired"}, Services: map[string]Settings{
		"wifi":  {"HTTPEnable": float64(0), "ProxyAutoConfigEnable": float64(1), "ProxyAutoConfigURLString": "https://private/pac"},
		"wired": {"HTTPEnable": float64(0)},
	}}}
	return NewWithDriver(d), d
}
func TestApplyRestoreExactOriginalAndIdempotence(t *testing.T) {
	m, d := fixture()
	original := clone(d.s)
	if err := m.Apply("127.0.0.1:12808", "localhost;127.*;[::1];<local>;10.*"); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.State(); !ok || err != nil {
		t.Fatalf("state %v %v", ok, err)
	}
	if d.s.Services["wifi"]["HTTPProxy"] != "127.0.0.1" || d.s.Services["wifi"]["ProxyAutoConfigEnable"] != float64(0) {
		t.Fatal("proxy/PAC not configured")
	}
	if err := m.Apply("127.0.0.1:12808", "localhost;127.*;[::1];<local>;10.*"); err != nil {
		t.Fatal(err)
	}
	if err := m.Restore(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.s, original) || d.r != nil {
		t.Fatal("original absent fields or PAC not restored")
	}
}
func TestPartialCommitAndJournalCommitFailureRollback(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "journal", true: "partial OS commit"}[partial], func(t *testing.T) {
			m, d := fixture()
			original := clone(d.s)
			if partial {
				d.failCAS = true
				d.partial = true
			} else {
				d.failSave = true
			}
			if m.Apply("127.0.0.1:12808", "") == nil {
				t.Fatal("failure acknowledged")
			}
			if !reflect.DeepEqual(d.s, original) || d.r != nil {
				t.Fatal("failed apply did not rollback")
			}
		})
	}
}
func TestRestorePreservesCompetingPolicyAndRestoresRemainingOwnedServices(t *testing.T) {
	m, d := fixture()
	if err := m.Apply("127.0.0.1:12808", ""); err != nil {
		t.Fatal(err)
	}
	d.s.Services["wifi"]["HTTPProxy"] = "competitor"
	if ok, err := m.State(); ok || !errors.Is(err, winproxy.ErrOwnershipLost) {
		t.Fatal("lost ownership not detected")
	}
	if err := m.Restore(); !errors.Is(err, winproxy.ErrOwnershipLost) {
		t.Fatal(err)
	}
	if d.s.Services["wifi"]["HTTPProxy"] != "competitor" || d.s.Services["wifi"]["ProxyAutoConfigEnable"] != float64(0) || d.s.Services["wired"]["HTTPEnable"] != float64(0) {
		t.Fatal("restore overwrote competitor or stranded owned settings")
	}
	if err := m.Relinquish(); err != nil {
		t.Fatal(err)
	}
}

func TestExternalManualProxyNeverReactivatesOriginalPAC(t *testing.T) {
	m, d := fixture()
	if err := m.Apply("127.0.0.1:12808", ""); err != nil {
		t.Fatal(err)
	}
	d.s.Services["wifi"]["HTTPProxy"] = "external.example"
	d.s.Services["wifi"]["HTTPPort"] = float64(8080)
	if err := m.Restore(); !errors.Is(err, winproxy.ErrOwnershipLost) {
		t.Fatal(err)
	}
	s := d.s.Services["wifi"]
	if !equal(s["HTTPEnable"], 1) || !equal(s["ProxyAutoConfigEnable"], 0) || s["HTTPProxy"] != "external.example" || !equal(s["HTTPPort"], 8080) {
		t.Fatal("restoration changed external manual/PAC policy")
	}
}

func TestBypassOnlyCompetitionRestoresBothOwnedEndpointsBeforeCleanup(t *testing.T) {
	m, d := fixture()
	d.s.Active = []string{"wifi"}
	if err := m.Apply("127.0.0.1:12808", ""); err != nil {
		t.Fatal(err)
	}
	d.s.Services["wifi"]["ExceptionsList"] = []any{"external.example"}
	if !errors.Is(m.Relinquish(), winproxy.ErrRecoveryRequired) {
		t.Fatal("forgot endpoints after bypass competition")
	}
	if err := m.Restore(); !errors.Is(err, winproxy.ErrOwnershipLost) {
		t.Fatal(err)
	}
	for _, prefix := range []string{"HTTP", "HTTPS"} {
		if d.s.Services["wifi"][prefix+"Proxy"] == "127.0.0.1" {
			t.Fatalf("stranded %s loopback", prefix)
		}
	}
	if !equal(d.s.Services["wifi"]["ExceptionsList"], []string{"external.example"}) {
		t.Fatal("external bypass overwritten")
	}
	if d.r != nil {
		t.Fatal("fully resolved endpoints left recovery active")
	}
	if err := NewWithDriver(d).Recover(); err != nil {
		t.Fatal(err)
	}
	if err := m.Relinquish(); err != nil {
		t.Fatal(err)
	}
	if active, err := m.State(); active || err != nil {
		t.Fatal("false active cleanup", active, err)
	}
}
func TestHTTPCompetitionRestoresHTTPSWithoutReactivatingPACOnRecovery(t *testing.T) {
	m, d := fixture()
	d.s.Active = []string{"wifi"}
	if err := m.Apply("127.0.0.1:12808", ""); err != nil {
		t.Fatal(err)
	}
	d.s.Services["wifi"]["HTTPProxy"] = "external.example"
	d.s.Services["wifi"]["HTTPPort"] = float64(8080)
	if !errors.Is(m.Relinquish(), winproxy.ErrRecoveryRequired) {
		t.Fatal("forgot still-owned HTTPS")
	}
	if err := NewWithDriver(d).Recover(); !errors.Is(err, winproxy.ErrOwnershipLost) {
		t.Fatal(err)
	}
	s := d.s.Services["wifi"]
	if s["HTTPSProxy"] == "127.0.0.1" || !equal(s["HTTPEnable"], 1) || s["HTTPProxy"] != "external.example" || !equal(s["HTTPPort"], 8080) || !equal(s["ProxyAutoConfigEnable"], 0) {
		t.Fatal("stranded HTTPS or changed external manual/PAC behavior")
	}
	if d.r != nil {
		t.Fatal("safe endpoint cleanup was not finalized")
	}
	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	if err := m.Relinquish(); err != nil {
		t.Fatal(err)
	}
}
func TestAmbiguousLoopbackRetainsJournalAcrossRecoveryAndRelinquish(t *testing.T) {
	m, d := fixture()
	d.s.Active = []string{"wifi"}
	if err := m.Apply("127.0.0.1:12808", ""); err != nil {
		t.Fatal(err)
	}
	// A competing enable value prevents grouped restoration, but the endpoint
	// still belongs to BlueGrass. No cleanup path may forget this leftover.
	d.s.Services["wifi"]["HTTPEnable"] = false
	if err := m.Restore(); !errors.Is(err, winproxy.ErrRecoveryRequired) || d.r == nil {
		t.Fatal("forgot ambiguous loopback", err)
	}
	if err := m.Relinquish(); !errors.Is(err, winproxy.ErrRecoveryRequired) || d.r == nil {
		t.Fatal("relinquished unresolved loopback", err)
	}
	if err := NewWithDriver(d).Recover(); !errors.Is(err, winproxy.ErrRecoveryRequired) || d.r == nil {
		t.Fatal("recovery falsely succeeded", err)
	}
	if d.s.Services["wifi"]["HTTPSProxy"] == "127.0.0.1" {
		t.Fatal("failed to restore independently owned HTTPS")
	}
	d.s.Services["wifi"]["HTTPProxy"] = "external.example"
	d.s.Services["wifi"]["HTTPPort"] = float64(8080)
	if err := m.Recover(); !errors.Is(err, winproxy.ErrOwnershipLost) {
		t.Fatal(err)
	}
	if d.r != nil {
		t.Fatal("journal persisted after endpoint was resolved")
	}
	if err := m.Relinquish(); err != nil {
		t.Fatal(err)
	}
}
func TestCompetingWriterBetweenReadAndWriteIsNeverOverwritten(t *testing.T) {
	m, d := fixture()
	d.race = true
	if err := m.Apply("127.0.0.1:12808", ""); !errors.Is(err, winproxy.ErrOwnershipLost) {
		t.Fatal(err)
	}
	if d.s.Services["wifi"]["HTTPProxy"] != "competitor" {
		t.Fatal("competitor overwritten")
	}
}
func TestRecoveryAfterCrashAndRefusalToForgetOwnedSettings(t *testing.T) {
	m, d := fixture()
	original := clone(d.s)
	if err := m.Apply("127.0.0.1:12808", ""); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.Relinquish(), winproxy.ErrRecoveryRequired) {
		t.Fatal("forgot active backup")
	}
	m = NewWithDriver(d)
	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.s, original) || d.r != nil {
		t.Fatal("crash recovery failed")
	}
}
func TestNetworkServiceChangeInvalidatesAppliedClaim(t *testing.T) {
	m, d := fixture()
	if err := m.Apply("127.0.0.1:12808", ""); err != nil {
		t.Fatal(err)
	}
	d.s.Active = []string{"wifi"}
	if ok, err := m.State(); ok || !errors.Is(err, winproxy.ErrOwnershipLost) {
		t.Fatal("new network claimed protected")
	}
}
func TestRejectsNonLoopbackOrNonStandardPortWithoutChanges(t *testing.T) {
	for _, address := range []string{"0.0.0.0:12808", "127.0.0.1:80", "127.0.0.1:12808;echo secret"} {
		m, d := fixture()
		original := clone(d.s)
		if !errors.Is(m.Apply(address, ""), winproxy.ErrInvalidSettings) || d.r != nil || !reflect.DeepEqual(d.s, original) {
			t.Fatal("invalid listener accepted")
		}
	}
}
