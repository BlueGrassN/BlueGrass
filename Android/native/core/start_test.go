package bridge

import "testing"

func TestInvalidStartDoesNotOpenNetworkOrConsumeFD(t *testing.T) {
	f := &fakePlatform{resolved: "8.8.4.4"}
	if s, err := Start(validConfig, -1, f); s != nil || err == nil {
		t.Fatal("invalid fd accepted")
	}
	if len(f.protected) != 0 {
		t.Fatal("invalid start dialed")
	}
}
