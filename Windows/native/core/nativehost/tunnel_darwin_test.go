//go:build darwin

package nativehost

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"bluegrass/native/core/mactun"
)

func TestDarwinWorkerInitializationFailureHasBoundedSafeReply(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{mactun.ErrOwner, "OWNER_MISMATCH"},
		{errors.New("private filesystem or command detail"), "RECOVERY_REQUIRED"},
	} {
		var output bytes.Buffer
		err := rejectDarwinTunnelStartup(strings.NewReader("{\"id\":\"hello\",\"command\":\"Hello\"}\n"), &output, tc.err)
		var response Response
		if err == nil || json.Unmarshal(output.Bytes(), &response) != nil || response.ID != "hello" || response.OK || response.Error != tc.code || strings.Contains(output.String(), "private") {
			t.Fatal("unsafe startup rejection", output.String(), err)
		}
	}
	for _, input := range []string{"", "{}\n", "{\"id\":\"x\",\"command\":\"Disconnect\"}\n", strings.Repeat("x", 65538)} {
		var output bytes.Buffer
		if rejectDarwinTunnelStartup(strings.NewReader(input), &output, mactun.ErrRecovery) == nil || output.Len() != 0 {
			t.Fatal("invalid initial request accepted")
		}
	}
}
