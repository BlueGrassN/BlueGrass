package diagnostics

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestObservationProxyExitNeedsNoExpectedAndAcceptsChangedPublicIP(t *testing.T) {
	for _, expected := range []string{"", "invalid", "1.1.1.1"} {
		for _, ip := range []string{"9.9.9.9", "8.8.4.4"} {
			_, pool, dial := tlsFixture(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"ip":"`+ip+`"}`) })
			req := Request{Binding: binding(), ExpectedExitIP: expected, ExitEndpoints: []ExitEndpoint{{URL: "https://example.com", IPv4: "8.8.4.4"}}}
			r := result(t, (Runner{RootCAs: pool, SelectedDial: dial}).Run(context.Background(), req), "proxy_exit")
			if r.Status != Passed || r.Code != "observed_exit" || r.ObservedIP != ip {
				t.Fatalf("%+v", r)
			}
		}
	}
}

func TestIPv4OnlyDiagnosticRejectsIPv6AndContinuesFallback(t *testing.T) {
	_, pool, dial := tlsFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v6" {
			_, _ = io.WriteString(w, `{"ip":"2606:4700::1111"}`)
		} else {
			_, _ = io.WriteString(w, `{"ip":"9.9.9.9"}`)
		}
	})
	runner := Runner{RootCAs: pool, SelectedDial: dial}
	req := Request{Binding: binding(), ExitEndpoints: []ExitEndpoint{{URL: "https://example.com/v6", IPv4: "8.8.4.4"}, {URL: "https://example.com/v4", IPv4: "8.8.4.4"}}}
	r := result(t, runner.Run(context.Background(), req), "proxy_exit")
	if r.Status != Passed || r.Code != "observed_exit" || r.ObservedIP != "9.9.9.9" || r.Attempts != 2 {
		t.Fatal("IPv6 stopped fallback", r)
	}
	req.ExitEndpoints = req.ExitEndpoints[:1]
	r = result(t, runner.Run(context.Background(), req), "proxy_exit")
	if r.Status != Failed || r.ObservedIP != "" {
		t.Fatal("IPv6-only detector passed", r)
	}
}
