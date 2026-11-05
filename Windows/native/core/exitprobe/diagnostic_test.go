package exitprobe

import (
	"context"
	"errors"
	"testing"

	"bluegrass/native/core/diagnostics"
)

func TestDiagnosticUsesExistingProbeOnlyAndDoesNotClaimUDP(t *testing.T) {
	b := diagnostics.Binding{Connected: true, Generation: 8, ResourceID: "fixture-resource"}
	for _, ip := range []string{"1.1.1.1", "9.9.9.9", "8.8.4.4"} {
		calls := 0
		r := Diagnostic(context.Background(), b, "1.1.1.1", func(ctx context.Context) (string, error) {
			calls++
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("no deadline")
			}
			return ip, nil
		})
		if calls != 1 || r.Generation != 8 || r.ResourceFingerprint == "" || r.Cancelled {
			t.Fatal("missing bound observation")
		}
		for _, v := range r.Results {
			if v.ID == "proxy_exit" {
				if v.ObservedIP != ip {
					t.Fatal("lost reliable observation")
				}
				if v.Status != diagnostics.Passed || v.Code != "observed_exit" {
					t.Fatal("observation contract")
				}
			} else if v.Status != diagnostics.Skipped {
				t.Fatal("unmeasured feature claimed")
			}
		}
	}
	r := Diagnostic(context.Background(), b, "1.1.1.1", func(context.Context) (string, error) { return "", errors.New("fixture-secret") })
	for _, v := range r.Results {
		if v.Code == "fixture-secret" || v.ObservedIP != "" {
			t.Fatal("error leaked")
		}
	}
}

func TestDiagnosticInactiveAndCancellationCannotPass(t *testing.T) {
	b := diagnostics.Binding{Connected: false, Generation: 8, ResourceID: "fixture-resource"}
	r := Diagnostic(context.Background(), b, "1.1.1.1", func(context.Context) (string, error) { t.Fatal("inactive probe"); return "", nil })
	for _, v := range r.Results {
		if v.Status != diagnostics.Skipped {
			t.Fatal("inactive passed")
		}
	}
	b.Connected = true
	ctx, cancel := context.WithCancel(context.Background())
	r = Diagnostic(ctx, b, "1.1.1.1", func(context.Context) (string, error) { cancel(); return "1.1.1.1", nil })
	if !r.Cancelled {
		t.Fatal("late result accepted")
	}
	for _, v := range r.Results {
		if v.Status != diagnostics.Skipped || v.ObservedIP != "" {
			t.Fatal("cancelled evidence retained")
		}
	}
}
