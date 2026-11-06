package bridge

import (
	"errors"
	"testing"
)

type fakePlatform struct {
	protected []int32
	fail      bool
	resolved  string
}

func (f *fakePlatform) IsValid() bool { return true }

func (f *fakePlatform) Protect(fd int32) error {
	f.protected = append(f.protected, fd)
	if f.fail {
		return errors.New("FAKE_SECRET")
	}
	return nil
}
func (f *fakePlatform) Resolve(string) (string, error) { return f.resolved, nil }
func (*fakePlatform) NetworkInfo() (string, error) {
	return `{"name":"wlan0","index":7,"mtu":1500,"addresses":["192.168.8.5/24"],"dnsServers":["192.168.8.1"],"domains":[]}`, nil
}
func TestPlatformProtectionFailureNeverSucceedsOrRetainsMessage(t *testing.T) {
	f := &fakePlatform{fail: true}
	p := &platformBridge{callback: f, info: fixtureNetwork}
	err := p.AutoDetectInterfaceControl(12)
	if err == nil || err.Error() != "SOCKET_PROTECTION_FAILED" {
		t.Fatalf("unexpected protection result: %v", err)
	}
	if len(f.protected) != 1 || f.protected[0] != 12 {
		t.Fatal("wrong fd protected")
	}
}
func TestResolvedEntryRequiresPublicIPv4(t *testing.T) {
	for _, raw := range []string{"", "::1", "192.168.1.1", "198.18.0.1", "2001:4860:4860::8888", "invalid"} {
		if _, err := resolveEntry(&fakePlatform{resolved: raw}, "entry.example.invalid"); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	ip, err := resolveEntry(&fakePlatform{resolved: "192.168.1.1,8.8.4.4"}, "entry.example.invalid")
	if err != nil || ip.String() != "8.8.4.4" {
		t.Fatal("public candidate unavailable")
	}
}
