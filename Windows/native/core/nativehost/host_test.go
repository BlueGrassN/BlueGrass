package nativehost

import (
	"bytes"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func TestHelloExposesOnlyIndependentLocalProxy(t *testing.T) {
	for range 2 {
		var output bytes.Buffer
		if err := Run(strings.NewReader("{\"id\":\"hello\",\"command\":\"Hello\"}\n"), &output); err != nil {
			t.Fatal(err)
		}
		var reply Response
		if json.Unmarshal(output.Bytes(), &reply) != nil {
			t.Fatal("invalid reply")
		}
		if !reply.OK || reply.Snapshot["systemProxyApplied"] != false || reply.Snapshot["businessGateOpen"] != false {
			t.Fatal("unexpected network claim")
		}
		if reply.Snapshot["proxyAddress"] != "127.0.0.1:12808" {
			t.Fatal("local proxy did not retain its fixed loopback address")
		}
	}
}

func TestOccupiedLocalProxyPortDoesNotFallBack(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:12808")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var output bytes.Buffer
	err = Run(strings.NewReader("{\"id\":\"hello\",\"command\":\"Hello\"}\n"), &output)
	if err == nil || err.Error() != "LOCAL_LISTENER_FAILED" || output.Len() != 0 {
		t.Fatal("occupied fixed port must not start a proxy on another port")
	}
	conn, err := net.Dial("tcp4", listener.Addr().String())
	if err != nil {
		t.Fatal("existing listener was disturbed")
	}
	conn.Close()
}
func TestMalformedInputCannotOpenGate(t *testing.T) {
	for _, line := range []string{`{"id":"x","command":"Hello","extra":true}`, `{"id":"x","command":"Hello"} {}`, `{"id":"x","command":"Connect","command":"Hello"}`, strings.Repeat("x", 65538)} {
		var output bytes.Buffer
		if Run(strings.NewReader(line+"\n"), &output) == nil {
			t.Fatal("bad input accepted")
		}
	}
}
func TestTunRejectedWithoutAnySystemChange(t *testing.T) {
	h, err := New(0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	r := h.Handle(Request{ID: "test", Command: "Connect", Profile: json.RawMessage(`{"mode":"tun"}`)})
	if r.OK || r.Snapshot["systemProxyApplied"] != false {
		t.Fatal("unsupported mode was accepted")
	}
}
