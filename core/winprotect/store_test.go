package winprotect

import (
	"bytes"
	"testing"
)

func TestIntentCodecRejectsAmbiguousOrTruncatedRecovery(t *testing.T) {
	r := intent{Version: 1, ProtectIntent: true, OwnerSID: testSID, AccountKey: "a", ResourceID: "r", Mode: "tun", Since: "2026-10-12T00:00:00Z"}
	b, err := encodeIntent(r)
	if err != nil {
		t.Fatal(err)
	}
	v, err := decodeIntent(b)
	if err != nil || *v != r {
		t.Fatal("owner was not retained")
	}
	for _, bad := range [][]byte{nil, b[:len(b)-1], []byte(`{"protectIntent":false}`), append(append([]byte(nil), b...), '\n'), bytes.Replace(b, []byte(`"protectIntent":true`), []byte(`"protectIntent":false,"protectIntent":true`), 1), bytes.Replace(b, []byte(`"version":1`), []byte(`"version":2`), 1)} {
		if _, err := decodeIntent(bad); err == nil {
			t.Fatal("accepted ambiguous journal")
		}
	}
	tombstone, err := encodeIntent(intent{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := decodeIntent(tombstone); err != nil || v.ProtectIntent || v.OwnerSID != "" {
		t.Fatal("inactive tombstone invalid")
	}
}
