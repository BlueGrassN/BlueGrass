package exitprobe

import (
	"context"
	"testing"

	"bluegrass/native/core/diagnostics"
)

func TestObservationDiagnosticIgnoresExpectedAndReportsCurrentIP(t *testing.T) {
	binding := diagnostics.Binding{Connected: true, Generation: 1, ResourceID: "fixture"}
	for _, expected := range []string{"", "invalid", "1.1.1.1"} {
		for _, ip := range []string{"9.9.9.9", "8.8.4.4"} {
			report := Diagnostic(context.Background(), binding, expected, func(context.Context) (string, error) { return ip, nil })
			found := false
			for _, result := range report.Results {
				if result.ID == "proxy_exit" {
					found = true
					if result.Status != diagnostics.Passed || result.Code != "observed_exit" || result.ObservedIP != ip {
						t.Fatalf("%+v", result)
					}
				}
			}
			if !found {
				t.Fatal("missing exit result")
			}
		}
	}
}

func TestIPv4OnlyObservationRejectsIPv6(t *testing.T) {
	for _, ip := range []string{"2606:4700::1111", "2001:db8::1", "3fff::1", "::ffff:8.8.8.8"} {
		if ValidObservedAddress(ip) {
			t.Fatal("IPv6 accepted as IPv4 observation", ip)
		}
		report := Diagnostic(context.Background(), diagnostics.Binding{Connected: true, Generation: 1, ResourceID: "fixture"}, "", func(context.Context) (string, error) { return ip, nil })
		for _, result := range report.Results {
			if result.ID == "proxy_exit" && (result.Status != diagnostics.Failed || result.ObservedIP != "") {
				t.Fatal("IPv6 diagnostic published", result)
			}
		}
	}
}
