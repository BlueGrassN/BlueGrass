package cmdupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestUnconfiguredHelperDoesNotOpenStorageOrNetwork(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), strings.NewReader(`{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"check"}`), &out); err != nil {
		t.Fatal(err)
	}
	var r reply
	if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.OK || r.Error != "UPDATE_UNCONFIGURED" {
		t.Fatal(out.String(), err)
	}
}
func TestProtocolRejectsOverridesUnknownFieldsAndTrailingRequests(t *testing.T) {
	for _, raw := range []string{
		`{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"check","keys":{}}`,
		`{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"check","manifestUrl":"https://evil.test/"}`,
		`{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"check","OP":"launch"}`,
		`{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"check","op":"launch"}`,
		`{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"launch","candidateId":"../evil","installConsent":true}`,
		`{"v":1,"id":"0123456789abcdef0123456789abcdef","op":"check"} {}`,
		strings.Repeat(" ", 4097),
	} {
		var out bytes.Buffer
		_ = Run(context.Background(), strings.NewReader(raw), &out)
		var r reply
		if err := json.Unmarshal(out.Bytes(), &r); err != nil || r.Error != "UPDATE_INVALID_REQUEST" {
			t.Fatalf("accepted request: %s", out.String())
		}
	}
}
