package httpapi

import (
	"crypto/tls"
	"encoding/json"
	"strings"
	"testing"

	"bluegrass/client-api/internal/model"
)

func TestStrictJSONAndInputValidationBeforeAuthentication(t *testing.T) {
	loginBody := func(in model.LoginInput) string {
		b, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	valid := model.LoginInput{Username: "alice", Password: "private-password", DeviceID: uuid, DeviceName: "Laptop"}
	cases := []struct {
		name, path, body string
		status           int
	}{
		{"empty", "/auth/login", "", 422}, {"malformed", "/auth/login", `{"password":"private-password"`, 422},
		{"null", "/auth/login", "null", 422}, {"array", "/auth/login", "[]", 422},
		{"unknown", "/auth/login", strings.TrimSuffix(loginJSON, "}") + `,"customer_id":999}`, 422},
		{"second-document", "/auth/login", loginJSON + ` {}`, 422}, {"trailing-null", "/auth/login", loginJSON + ` null`, 422},
		{"trailing-garbage", "/auth/login", loginJSON + ` private-password`, 422},
		{"oversized", "/auth/login", loginJSON + strings.Repeat(" ", 4097), 413},
		{"refresh-unknown", "/auth/refresh", strings.TrimSuffix(refreshJSON, "}") + `,"password":"private-password"}`, 422},
		{"refresh-second", "/auth/refresh", refreshJSON + ` {}`, 422},
		{"refresh-null", "/auth/refresh", "null", 422},
		{"refresh-missing-token", "/auth/refresh", `{"request_id":"` + uuid + `"}`, 422},
		{"refresh-empty-token", "/auth/refresh", `{"refresh_token":"","request_id":"` + uuid + `"}`, 422},
		{"refresh-missing-id", "/auth/refresh", `{"refresh_token":"private-refresh-token"}`, 422},
		{"refresh-invalid-id", "/auth/refresh", `{"refresh_token":"private-refresh-token","request_id":"bad-uuid"}`, 422},
		{"refresh-zero-id", "/auth/refresh", `{"refresh_token":"private-refresh-token","request_id":"00000000-0000-0000-0000-000000000000"}`, 422},
		{"refresh-oversized", "/auth/refresh", refreshJSON + strings.Repeat(" ", 4097), 413},
	}
	for _, tc := range []struct {
		name   string
		change func(*model.LoginInput)
	}{
		{"empty-username", func(in *model.LoginInput) { in.Username = "" }},
		{"blank-username", func(in *model.LoginInput) { in.Username = "   " }},
		{"long-username", func(in *model.LoginInput) { in.Username = strings.Repeat("a", 1025) }},
		{"empty-password", func(in *model.LoginInput) { in.Password = "" }},
		{"long-password", func(in *model.LoginInput) { in.Password = strings.Repeat("p", 2049) }},
		{"missing-device", func(in *model.LoginInput) { in.DeviceID = "" }},
		{"invalid-device", func(in *model.LoginInput) { in.DeviceID = "private-password" }},
		{"zero-device", func(in *model.LoginInput) { in.DeviceID = "00000000-0000-0000-0000-000000000000" }},
		{"long-name", func(in *model.LoginInput) { in.DeviceName = strings.Repeat("x", 1025) }},
		{"long-unicode-name", func(in *model.LoginInput) { in.DeviceName = strings.Repeat("界", 513) }},
	} {
		in := valid
		tc.change(&in)
		cases = append(cases, struct {
			name, path, body string
			status           int
		}{tc.name, "/auth/login", loginBody(in), 422})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAuth{}
			r := request("POST", tc.path, tc.body)
			// Unknown Content-Length must not disable streaming size enforcement.
			r.ContentLength = -1
			w := serve(newHandler(t, a, nil, Config{}), r)
			got := checkResponse(t, w, tc.status)
			if tc.status == 413 && got.Error != nil && got.Error.Code != "payload_too_large" {
				t.Errorf("oversized body code = %q, want payload_too_large", got.Error.Code)
			}
			checkNoSecrets(t, w, "private-password", "private-refresh-token", "bad-uuid")
			if a.loginCalls.Load() != 0 || a.refreshCalls.Load() != 0 || a.authCalls.Load() != 0 {
				t.Error("invalid body reached authentication")
			}
		})
	}
}

func TestJSONBodyLimitIncludesTrailingWhitespace(t *testing.T) {
	for _, pathBody := range []struct{ path, body string }{{"/auth/login", loginJSON}, {"/auth/refresh", refreshJSON}} {
		for _, size := range []int{4096, 4097} {
			a := &fakeAuth{}
			body := pathBody.body + strings.Repeat(" ", size-len(pathBody.body))
			w := serve(newHandler(t, a, nil, Config{}), request("POST", pathBody.path, body))
			want := 200
			if size > 4096 {
				want = 413
			}
			checkResponse(t, w, want)
			calls := a.loginCalls.Load() + a.refreshCalls.Load()
			if (size == 4096 && calls != 1) || (size == 4097 && calls != 0) {
				t.Errorf("size=%d auth calls=%d", size, calls)
			}
		}
	}
}

func TestUnicodeLoginAndOptionalDeviceName(t *testing.T) {
	for _, body := range []string{
		`{"username":"用户","password":"密 码","device_id":"` + uuid + `","device_name":"我的电脑"}`,
		`{"username":"alice","password":" password with spaces ","device_id":"` + uuid + `"}`,
	} {
		a := &fakeAuth{}
		checkResponse(t, serve(newHandler(t, a, nil, Config{}), request("POST", "/auth/login", body)), 200)
		if a.loginCalls.Load() != 1 {
			t.Error("valid Unicode/optional name input was not accepted")
		}
	}
}

func TestHTTPSOnlyTrustsExplicitProxyCIDRs(t *testing.T) {
	for _, tc := range []struct {
		name, remote, forwarded string
		tls, trusted            bool
		status                  int
	}{
		{"native-tls", "198.51.100.9:10", "", true, false, 200},
		{"plain", "198.51.100.9:10", "", false, false, 403},
		{"spoofed", "198.51.100.9:10", "https", false, true, 403},
		{"no-implicit-loopback-trust", "127.0.0.1:10", "https", false, false, 403},
		{"trusted-v4", "127.0.0.1:10", "https", false, true, 200},
		{"trusted-v6", "[::1]:10", "https", false, true, 200},
		{"trusted-http", "127.0.0.1:10", "http", false, true, 403},
		{"trusted-chain", "127.0.0.1:10", "https,http", false, true, 403},
		{"malformed-peer", "not-an-ip", "https", false, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{RequireHTTPS: true}
			if tc.trusted {
				cfg.TrustedProxyCIDRs = []string{"127.0.0.0/8", "::1/128"}
			}
			a := &fakeAuth{}
			r := request("POST", "/auth/login", loginJSON)
			r.RemoteAddr = tc.remote
			r.Header.Set("X-Forwarded-Proto", tc.forwarded)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			w := serve(newHandler(t, a, nil, cfg), r)
			checkResponse(t, w, tc.status)
			if tc.status != 200 && a.loginCalls.Load() != 0 {
				t.Error("insecure request reached authentication")
			}
		})
	}
}

func TestIPThrottleUsesTrustedRealIPOrActualPeer(t *testing.T) {
	for _, tc := range []struct {
		name, firstPeer, secondPeer, firstIP, secondIP string
		trusted                                        bool
		status                                         int
	}{
		{"untrusted-real-ip-ignored", "198.51.100.5:1", "198.51.100.5:2", "192.0.2.1", "192.0.2.2", true, 429},
		{"no-implicit-proxy-trust", "127.0.0.1:1", "127.0.0.1:2", "192.0.2.1", "192.0.2.2", false, 429},
		{"trusted-distinct-client", "127.0.0.1:1", "127.0.0.1:2", "192.0.2.1", "192.0.2.2", true, 200},
		{"trusted-same-client", "127.0.0.1:1", "127.0.0.2:2", "192.0.2.1", "192.0.2.1", true, 429},
		{"invalid-real-ip-fallback", "127.0.0.1:1", "127.0.0.1:2", "invalid-one", "invalid-two", true, 429},
		{"ip-chain-invalid", "127.0.0.1:1", "127.0.0.1:2", "192.0.2.1,192.0.2.2", "192.0.2.3,192.0.2.4", true, 429},
		{"forwarded-for-not-used", "127.0.0.1:1", "127.0.0.1:2", "", "", true, 429},
		{"ipv6-peer-canonical", "[2001:db8::1]:1", "[2001:0db8:0:0:0:0:0:1]:2", "", "", false, 429},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{LoginPerMinute: 1}
			if tc.trusted {
				cfg.TrustedProxyCIDRs = []string{"127.0.0.0/8"}
			}
			h := newHandler(t, nil, nil, cfg)
			first := request("POST", "/auth/login", loginJSON)
			first.RemoteAddr = tc.firstPeer
			first.Header.Set("X-Real-IP", tc.firstIP)
			first.Header.Set("X-Forwarded-For", "192.0.2.30")
			checkResponse(t, serve(h, first), 200)
			second := request("POST", "/auth/login", loginJSON)
			second.RemoteAddr = tc.secondPeer
			second.Header.Set("X-Real-IP", tc.secondIP)
			second.Header.Set("X-Forwarded-For", "192.0.2.40")
			checkResponse(t, serve(h, second), tc.status)
		})
	}
}
