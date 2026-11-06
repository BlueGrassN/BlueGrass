package control

import (
	"context"
	"fmt"
	"reflect"
	"testing"
)

func TestReceiptWindowAllowsLongSessionUpdatesAndSafetyCommands(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	var last ControlRequest
	for seq := uint64(2); seq <= 2*maxHistory+17; seq++ {
		last = request(ApplyResourceUpdate, fmt.Sprintf("refresh-%d", seq), seq)
		last.Params.Update = &ResourceUpdate{Revision: seq}
		apply(t, c, last)
		if len(c.receipts) > maxHistory {
			t.Fatal("receipt storage grew beyond its bound")
		}
	}
	if !c.Snapshot().BusinessGateOpen {
		t.Fatal("routine refreshes stopped the verified session")
	}
	for i, command := range []Command{Pause, Disconnect, ReleaseProtection} {
		a := apply(t, c, request(command, string(command), last.Sequence+uint64(i)+1))
		if a.Snapshot.BusinessGateOpen || !a.Snapshot.GateKnown {
			t.Fatalf("%s did not confirm gate closure", command)
		}
		if a.Snapshot.ProtectionIntent != (command == Pause) {
			t.Fatalf("%s returned incorrect protection intent", command)
		}
	}
}

func TestReceiptWindowReplaysOriginalAckAndRejectsEvictedSequence(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	for seq := uint64(2); seq <= maxHistory+2; seq++ {
		r := request(ApplyResourceUpdate, fmt.Sprintf("refresh-%d", seq), seq)
		r.Params.Update = &ResourceUpdate{Revision: seq}
		apply(t, c, r)
	}
	r := request(Pause, "retained-pause", maxHistory+3)
	original := apply(t, c, r)
	apply(t, c, request(Disconnect, "later-disconnect", r.Sequence+1))
	before := f.history()
	replayed := apply(t, c, r)
	if !reflect.DeepEqual(original, replayed) || !reflect.DeepEqual(before, f.history()) {
		t.Fatal("retained retry changed its original acknowledgement or touched the engine")
	}
	// Returned snapshots must not let the caller mutate the retained receipt.
	replayed.Snapshot.Context.AttemptID = "caller-mutation"
	if got := apply(t, c, r); !reflect.DeepEqual(original, got) {
		t.Fatal("caller mutated the retained acknowledgement")
	}
	conflict := r
	conflict.Command = Disconnect
	ack, err := c.Apply(context.Background(), conflict)
	if err != nil || ack.ErrorCode != "REQUEST_CONFLICT" {
		t.Fatalf("changed retained request: %+v, %v", ack, err)
	}
	for _, old := range []ControlRequest{
		request(Connect, "connect", 1),
		request(Pause, "new-id-old-sequence", 1),
	} {
		ack, err = c.Apply(context.Background(), old)
		if err != nil || ack.Disposition != "stale" || ack.ErrorCode != "STALE_SEQUENCE" {
			t.Fatalf("evicted sequence was not denied: %+v, %v", ack, err)
		}
	}
	if !reflect.DeepEqual(before, f.history()) {
		t.Fatal("conflict or stale request touched the engine")
	}
}

func TestReceiptWindowForgetsRejectedRequestsWithoutReadmittingTheirSequence(t *testing.T) {
	c, f, _ := setup(t, false)
	connect(t, c, f)
	var first ControlRequest
	for seq := uint64(2); seq <= maxHistory+3; seq++ {
		r := request(ApplyResourceUpdate, fmt.Sprintf("stale-revision-%d", seq), seq)
		r.Params.Update = &ResourceUpdate{Revision: 1}
		if seq == 2 {
			first = r
		}
		ack, err := c.Apply(context.Background(), r)
		if err != nil || ack.ErrorCode != "STALE_RESOURCE_REVISION" {
			t.Fatalf("revision rejection changed at %d: %s, %v", seq, ack.ErrorCode, err)
		}
	}
	before := f.history()
	ack, err := c.Apply(context.Background(), first)
	if err != nil || ack.ErrorCode != "STALE_SEQUENCE" || !reflect.DeepEqual(before, f.history()) {
		t.Fatalf("forgotten rejected request was reevaluated: %+v, %v", ack, err)
	}
	// A fresh higher sequence can still pause after a flood of rejected updates.
	apply(t, c, request(Pause, "pause-after-rejections", maxHistory+4))
}

func TestReceiptWindowResetsWithAuthenticatedOwnerRebind(t *testing.T) {
	c, _, _ := setup(t, false)
	for seq := uint64(1); seq <= maxHistory+3; seq++ {
		apply(t, c, request(GetStatus, fmt.Sprintf("status-%d", seq), seq))
	}
	next := owner()
	next.ClientEpoch = "client-b"
	if err := c.RebindOwner(context.Background(), owner(), next); err != nil {
		t.Fatal(err)
	}
	// Reuse IDs in the new epoch and wrap the window again. Old ring entries
	// must not delete a new owner's receipts or retain an old sequence floor.
	var r ControlRequest
	for seq := uint64(1); seq <= maxHistory+3; seq++ {
		r = request(GetStatus, fmt.Sprintf("status-%d", seq), seq)
		r.Context.ClientEpoch = next.ClientEpoch
		apply(t, c, r)
	}
	original := apply(t, c, r)
	if got := apply(t, c, r); !reflect.DeepEqual(original, got) {
		t.Fatal("new owner lost retry idempotence")
	}
	old, err := c.Apply(context.Background(), request(GetStatus, r.RequestID, r.Sequence))
	if err != nil || old.ErrorCode != "STALE_CONTEXT" {
		t.Fatal("old owner regained access to the rebound receipt window")
	}
}
