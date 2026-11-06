package macproxy

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"bluegrass/native/core/winproxy"
)

func pipeFixture(t *testing.T, reply string) *pipeDriver {
	t.Helper()
	requestsR, requestsW := io.Pipe()
	responsesR, responsesW := io.Pipe()
	d := newPipeDriver(requestsW, responsesR, func() { _ = requestsW.Close(); _ = responsesR.Close() }, time.Second)
	t.Cleanup(d.close)
	go func() {
		defer requestsR.Close()
		defer responsesW.Close()
		reader := bufio.NewReader(requestsR)
		_, _ = reader.ReadBytes('\n')
		_, _ = io.WriteString(responsesW, reply+"\n")
	}()
	return d
}

func TestMultipleHelperCallsCannotRenewTransactionDeadline(t *testing.T) {
	rr, rw := io.Pipe()
	sr, sw := io.Pipe()
	stopped := make(chan struct{})
	d := newPipeDriver(rw, sr, func() { close(stopped); _ = rw.Close(); _ = sr.Close() }, time.Second)
	defer d.close()
	d.beginOperation(time.Now().Add(60 * time.Millisecond))
	defer d.endOperation()
	go func() {
		defer rr.Close()
		defer sw.Close()
		reader := bufio.NewReader(rr)
		for {
			if _, err := reader.ReadBytes('\n'); err != nil {
				return
			}
			select {
			case <-time.After(35 * time.Millisecond):
			case <-stopped:
				return
			}
			if _, err := io.WriteString(sw, `{"ok":true}`+"\n"); err != nil {
				return
			}
		}
	}()
	start := time.Now()
	if err := d.Delete(); err != nil {
		t.Fatal(err)
	}
	if err := d.Delete(); !errors.Is(err, winproxy.ErrRecoveryRequired) {
		t.Fatal("second call renewed deadline", err)
	}
	select {
	case <-stopped:
	default:
		t.Fatal("timeout returned without terminating helper")
	}
	if time.Since(start) > 300*time.Millisecond {
		t.Fatal("transaction exceeded total budget")
	}
}
func TestHelperFailureOnlyPublishesSafeCodes(t *testing.T) {
	for _, tc := range []struct {
		reply string
		want  error
	}{
		{`{"ok":false,"error":"OWNERSHIP_LOST"}`, winproxy.ErrOwnershipLost},
		{`{"ok":false,"error":"password-and-node-secret"}`, winproxy.ErrRecoveryRequired},
		{`not-json-private-data`, winproxy.ErrRecoveryRequired},
		{`{"ok":true,"snapshot":{"active":[],"services":{}}}{"private":"secret"}`, winproxy.ErrRecoveryRequired},
	} {
		d := pipeFixture(t, tc.reply)
		_, err := d.Read()
		if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private-data") {
			t.Fatalf("unsafe/unmapped error: %v", err)
		}
	}
}
func TestHelperRequestsUsePrivatePipeAndFixedOperation(t *testing.T) {
	rr, rw := io.Pipe()
	sr, sw := io.Pipe()
	d := newPipeDriver(rw, sr, func() { _ = rw.Close(); _ = sr.Close() }, time.Second)
	defer d.close()
	got := make(chan map[string]any, 1)
	go func() {
		defer rr.Close()
		defer sw.Close()
		var request map[string]any
		_ = json.NewDecoder(rr).Decode(&request)
		got <- request
		_, _ = io.WriteString(sw, `{"ok":true}`+"\n")
	}()
	if err := d.Save(Record{Version: 1, Phase: "prepared"}); err != nil {
		t.Fatal(err)
	}
	request := <-got
	if request["operation"] != "save" || request["record"] == nil {
		t.Fatal("journal not sent through private IPC")
	}
}
func TestLongStatusBudgetUsesSupportedNativeFrameDeadline(t *testing.T) {
	rr, rw := io.Pipe()
	sr, sw := io.Pipe()
	d := newPipeDriver(rw, sr, func() { _ = rw.Close(); _ = sr.Close() }, 24*time.Second)
	defer d.close()
	d.beginOperation(time.Now().Add(29 * time.Second))
	defer d.endOperation()
	go func() {
		defer rr.Close()
		defer sw.Close()
		var q helperRequest
		_ = json.NewDecoder(rr).Decode(&q)
		if q.TimeoutMillis > 24_000 {
			_, _ = io.WriteString(sw, `{"ok":false,"error":"INVALID_SETTINGS"}`+"\n")
		} else {
			_, _ = io.WriteString(sw, `{"ok":true}`+"\n")
		}
	}()
	if err := d.Delete(); err != nil {
		t.Fatal("status sent a deadline rejected by native protocol", err)
	}
}
func TestUnresponsiveHelperFailsBoundedly(t *testing.T) {
	rr, rw := io.Pipe()
	sr, sw := io.Pipe()
	defer rr.Close()
	defer sw.Close()
	d := newPipeDriver(rw, sr, func() { _ = rw.Close(); _ = sr.Close() }, 20*time.Millisecond)
	defer d.close()
	start := time.Now()
	_, err := d.Read()
	if !errors.Is(err, winproxy.ErrRecoveryRequired) || time.Since(start) > time.Second {
		t.Fatal("helper wait unbounded")
	}
}
