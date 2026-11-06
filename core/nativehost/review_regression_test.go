package nativehost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"bluegrass/native/core/control"
	"bluegrass/native/core/engine"
)

type reviewFakeEngine struct{ opened chan struct{} }

func (f *reviewFakeEngine) Prepare(context.Context, engine.Profile) error { return nil }
func (f *reviewFakeEngine) ProbeExit(context.Context) (engine.ProbeResult, error) {
	return engine.ProbeResult{IPv4: "1.1.1.1", Authenticated: true, Reachable: true}, nil
}
func (f *reviewFakeEngine) SetBusinessGate(open bool) error {
	if open {
		select {
		case f.opened <- struct{}{}:
		default:
		}
	}
	return nil
}
func (f *reviewFakeEngine) Stop(context.Context) error { return nil }
func reviewHost(t *testing.T) (*Host, *reviewFakeEngine) {
	t.Helper()
	h, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	f := &reviewFakeEngine{opened: make(chan struct{}, 8)}
	h.owner = control.Owner{Environment: "test", DataSource: "fake", AccountKey: "account", ClientEpoch: h.client}
	h.controller, err = control.New(f, h.clock, h.runtime, h.owner)
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}
func reviewProfile(source string) json.RawMessage {
	data, _ := json.Marshal(engine.Profile{Key: engine.ResourceKey{Environment: "test", DataSource: "fake", AccountKey: "account", Kind: "asset", SourceID: source},
		Protocol: "socks5", Entry: engine.Entry{Host: "1.1.1.1", Port: 443}, ExpectedExitIPv4: "1.1.1.1", ConfigVersion: 1, ConfigFingerprint: "fixture", Mode: "system_proxy"})
	return data
}
func TestReviewResponseFitsAcceptedFrame(t *testing.T) {
	for _, size := range []int{32000, 32200, 32400, 32500} {
		h, f := reviewHost(t)
		request := Request{ID: strings.Repeat("i", 36), Command: "Connect", Profile: reviewProfile(strings.Repeat("x", size))}
		wire, _ := json.Marshal(request)
		reply := h.Handle(request)
		if !reply.OK {
			t.Logf("source=%d requestBytes=%d rejected=%s", size, len(wire), reply.Error)
			continue
		}
		select {
		case <-f.opened:
		case <-time.After(time.Second):
			t.Fatal("no fake gate")
		}
		reply = h.Handle(Request{ID: strings.Repeat("s", 36), Command: "GetStatus"})
		out, _ := json.Marshal(reply)
		t.Logf("source=%d requestBytes=%d responseBytes=%d", size, len(wire), len(out))
		if len(out) > 65536 {
			t.Errorf("accepted %d-byte request produces %d-byte IPC response", len(wire), len(out))
		}
	}
}
func TestReviewLatePauseTargetsCurrentSession(t *testing.T) {
	h, f := reviewHost(t)
	old := reviewProfile("resource-A")
	for i, profile := range []json.RawMessage{old, reviewProfile("resource-B")} {
		request := Request{ID: fmt.Sprint("connect-", i), Command: "Connect", Profile: profile}
		if i != 0 {
			current := h.context
			request.Command, request.Context = "SwitchResource", &current
		}
		reply := h.Handle(request)
		if !reply.OK {
			t.Fatal(reply.Error)
		}
		select {
		case <-f.opened:
		case <-time.After(time.Second):
			t.Fatal("no fake gate")
		}
	}
	reply := h.Handle(Request{ID: "late-pause-for-A", Command: "Pause", Profile: old})
	t.Logf("late Pause for A: ok=%v connection=%v current=%s", reply.OK, reply.Snapshot["connection"], h.context.ResourceKey.SourceID)
	if reply.OK && reply.Snapshot["connection"] == "paused" && h.context.ResourceKey.SourceID == "resource-B" {
		t.Error("stale Pause applied to B; supplied old profile/context has no effect")
	}
}
func TestReviewRunEOFCleansListener(t *testing.T) {
	input, writer := io.Pipe()
	defer writer.Close()
	output, reader := io.Pipe()
	defer output.Close()
	done := make(chan error, 1)
	go func() { done <- Run(input, reader); reader.Close() }()
	fmt.Fprintln(writer, `{"id":"hello","command":"Hello"}`)
	var reply Response
	if err := json.NewDecoder(output).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	address := reply.Snapshot["proxyAddress"].(string)
	writer.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("EOF did not exit")
	}
	conn, err := net.DialTimeout("tcp4", address, time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("EOF left listener alive")
	}
}
func TestReviewNative64KiBBoundary(t *testing.T) {
	base := `{"id":"hello","command":"Hello"}`
	for _, size := range []int{65536, 65537} {
		var output bytes.Buffer
		err := Run(strings.NewReader(base+strings.Repeat(" ", size-len(base))+"\n"), &output)
		t.Logf("size=%d accepted=%v", size, err == nil)
		if (size == 65536) != (err == nil) {
			t.Fatal("boundary mismatch")
		}
	}
}
