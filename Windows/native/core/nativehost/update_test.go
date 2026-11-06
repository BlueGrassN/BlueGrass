package nativehost

import (
	"encoding/json"
	"testing"
	"time"
)

func TestResourceRevocationPausesExistingHost(t *testing.T) {
	h, e := reviewHost(t)
	if r := h.Handle(Request{ID: "connect", Command: "Connect", Profile: reviewProfile("resource")}); !r.OK {
		t.Fatal(r)
	}
	select {
	case <-e.opened:
	case <-time.After(time.Second):
		t.Fatal("not ready")
	}
	r := h.Handle(Request{ID: "revoke", Command: "ApplyResourceUpdate", Update: json.RawMessage(`{"revision":2,"validUntilUtc":null,"unavailable":true}`)})
	if !r.OK || r.Snapshot["businessGateOpen"] != false || r.Snapshot["connection"] != "paused" {
		t.Fatal("revocation not applied", r)
	}
}
