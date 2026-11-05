//go:build windows

package nativehost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
)

type recoveryClient struct {
	reply  func(Request) ([]byte, error)
	closed bool
	calls  int
}

func (c *recoveryClient) Exchange(b []byte) ([]byte, error) {
	c.calls++
	var r Request
	_ = json.Unmarshal(b, &r)
	return c.reply(r)
}
func (c *recoveryClient) Close() error { c.closed = true; return nil }
func recoveryReply(r Request, intent bool) ([]byte, error) {
	return json.Marshal(Response{ID: r.ID, OK: true, Snapshot: map[string]any{"protectionIntent": intent, "gateKnown": true, "businessGateOpen": false, "actualMode": "none", "connection": "disconnected"}})
}
func TestInstalledRecoveryNeverDowngradesUncertaintyToLocal(t *testing.T) {
	for _, tc := range []struct {
		name, want                       string
		locationErr, installErr, dialErr error
		reply                            func(Request) ([]byte, error)
	}{
		{name: "location unavailable", want: "SERVICE_UNAVAILABLE", locationErr: errors.New("location")},
		{name: "signature invalid", want: "SERVICE_UNAVAILABLE", installErr: errors.New("signature")},
		{name: "service stopped", want: "SERVICE_UNAVAILABLE", dialErr: errors.New("stopped")},
		{name: "transport failed", want: "RECOVERY_REQUIRED", reply: func(Request) ([]byte, error) { return nil, errors.New("pipe") }},
		{name: "owner mismatch", want: "OWNER_MISMATCH", reply: func(r Request) ([]byte, error) { return json.Marshal(Response{ID: r.ID, Error: "OWNER_MISMATCH"}) }},
		{name: "busy", want: "SERVICE_BUSY", reply: func(r Request) ([]byte, error) { return json.Marshal(Response{ID: r.ID, Error: "SERVICE_BUSY"}) }},
		{name: "unknown denial", want: "RECOVERY_REQUIRED", reply: func(r Request) ([]byte, error) { return json.Marshal(Response{ID: r.ID, Error: "private error"}) }},
		{name: "missing intent", want: "RECOVERY_REQUIRED", reply: func(r Request) ([]byte, error) { return json.Marshal(Response{ID: r.ID, OK: true}) }},
		{name: "nonbool intent", want: "RECOVERY_REQUIRED", reply: func(r Request) ([]byte, error) {
			return json.Marshal(Response{ID: r.ID, OK: true, Snapshot: map[string]any{"protectionIntent": "false"}})
		}},
		{name: "wrong id", want: "RECOVERY_REQUIRED", reply: func(Request) ([]byte, error) { return recoveryReply(Request{ID: "wrong"}, false) }},
		{name: "malformed reply", want: "RECOVERY_REQUIRED", reply: func(Request) ([]byte, error) { return []byte("{"), nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &recoveryClient{reply: tc.reply}
			deps := recoveryDependencies{
				installed:    func() (bool, error) { return true, tc.locationErr },
				installation: func() (string, string, error) { return "root", "publisher", tc.installErr },
				dial: func(context.Context, string, string) (tunnelClient, error) {
					if tc.dialErr != nil {
						return nil, tc.dialErr
					}
					return client, nil
				},
			}
			var out bytes.Buffer
			handled, err := recoverTunnel(strings.NewReader("{\"id\":\"gui-hello\",\"command\":\"Hello\"}\n"), &out, deps)
			var reply Response
			decodeErr := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &reply)
			if !handled || err == nil || decodeErr != nil || reply.ID != "gui-hello" || reply.OK || reply.Error != tc.want || reply.Snapshot != nil {
				t.Fatalf("hidden recovery: handled=%v err=%v reply=%s", handled, err, out.String())
			}
			if client.calls > 0 && !client.closed {
				t.Fatal("probe lease not closed")
			}
		})
	}
}
func TestRecoveryOnlyFallsBackForDevOrPositiveAbsentIntent(t *testing.T) {
	for _, installed := range []bool{false, true} {
		client := &recoveryClient{reply: func(r Request) ([]byte, error) { return recoveryReply(r, false) }}
		deps := recoveryDependencies{installed: func() (bool, error) { return installed, nil }, installation: func() (string, string, error) {
			if !installed {
				t.Fatal("dev signature discovery")
			}
			return "root", "publisher", nil
		}, dial: func(context.Context, string, string) (tunnelClient, error) { return client, nil }}
		input := strings.NewReader("{\"id\":\"gui\",\"command\":\"Hello\"}\n")
		before := input.Len()
		var out bytes.Buffer
		handled, err := recoverTunnel(input, &out, deps)
		if handled || err != nil || input.Len() != before || out.Len() != 0 || installed && !client.closed {
			t.Fatalf("bad local fallback: %v %v", handled, err)
		}
	}
}
func TestRecoveryRetainsAuthenticatedClientForGUI(t *testing.T) {
	client := &recoveryClient{reply: func(r Request) ([]byte, error) { return recoveryReply(r, true) }}
	deps := recoveryDependencies{installed: func() (bool, error) { return true, nil }, installation: func() (string, string, error) { return "root", "publisher", nil }, dial: func(context.Context, string, string) (tunnelClient, error) { return client, nil }}
	var out bytes.Buffer
	handled, err := recoverTunnel(strings.NewReader("{\"id\":\"gui\",\"command\":\"Hello\"}\n"), &out, deps)
	var reply Response
	_ = json.Unmarshal(bytes.TrimSpace(out.Bytes()), &reply)
	if !handled || err != nil || client.calls != 2 || !client.closed || reply.ID != "gui" || reply.Snapshot["protectionIntent"] != true {
		t.Fatalf("recovery handoff: %v %v %+v", handled, err, reply)
	}
}
func TestInstalledLocationDoesNotDependOnSignatureOrResolvableRoot(t *testing.T) {
	for _, tc := range []struct {
		exe     string
		resolve func(string) (string, error)
		want    bool
		fail    bool
	}{
		{`C:\Program Files\BlueGrass\BlueGrass.Native.exe`, func(string) (string, error) { return "", fs.ErrPermission }, true, false},
		{`C:\Program Files\BlueGrass\sub\BlueGrass.Native.exe`, func(string) (string, error) { return "", fs.ErrPermission }, true, false},
		{`E:\workspace\APP\BlueGrass.Native.exe`, func(p string) (string, error) {
			if strings.Contains(p, "Program Files") {
				return "", fs.ErrNotExist
			}
			return p, nil
		}, false, false},
		{`E:\linked\BlueGrass.Native.exe`, func(p string) (string, error) {
			if strings.HasPrefix(p, `E:\`) {
				return `C:\Program Files\BlueGrass\BlueGrass.Native.exe`, nil
			}
			return p, nil
		}, true, false},
		{`E:\workspace\APP\BlueGrass.Native.exe`, func(string) (string, error) { return "", fs.ErrPermission }, false, true},
	} {
		got, err := installedLocation(tc.exe, `C:\Program Files`, tc.resolve)
		if got != tc.want || (err != nil) != tc.fail {
			t.Fatalf("location %q: %v %v", tc.exe, got, err)
		}
	}
}
