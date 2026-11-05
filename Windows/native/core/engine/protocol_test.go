package engine

import (
	"strings"
	"testing"

	"bluegrass/native/core/protocolconfig"
)

func TestEffectiveProtocolUsesExactlyOneCredentialSource(t *testing.T) {
	p := Profile{Protocol: "socks5", Auth: Auth{Username: "legacy-user", Password: "legacy-password"}}
	c, err := p.EffectiveProtocol()
	if err != nil || c.SOCKS5.Username != "legacy-user" || c.SOCKS5.Password != "legacy-password" {
		t.Fatal("legacy SOCKS mapping failed")
	}
	p.ProtocolConfig = &protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{Username: "typed-user", Password: "typed-password"}}
	if _, err = p.EffectiveProtocol(); err != protocolconfig.ErrInvalidConfig {
		t.Fatal("conflicting credential sources accepted")
	}
	p.Auth = Auth{}
	c, err = p.EffectiveProtocol()
	if err != nil || c.SOCKS5.Username != "typed-user" || c.SOCKS5.Password != "typed-password" {
		t.Fatal("explicit auth not selected")
	}
	p.Protocol = "https"
	if _, err = p.EffectiveProtocol(); err != protocolconfig.ErrInvalidConfig {
		t.Fatal("protocol mismatch accepted")
	}
	p.ProtocolConfig = nil
	if _, err = p.EffectiveProtocol(); err != protocolconfig.ErrInvalidConfig {
		t.Fatal("HTTPS invented from legacy auth")
	}
	p.Protocol = "socks5"
	for _, a := range []Auth{{Username: "user"}, {Password: "password"}, {Username: strings.Repeat("u", 256), Password: "password"}, {Username: "user", Password: "bad\npassword"}} {
		p.Auth = a
		if _, err = p.EffectiveProtocol(); err != protocolconfig.ErrInvalidConfig {
			t.Fatal("invalid RFC1929 credentials accepted")
		}
	}
}

func TestEffectiveProtocolRejectsAmbiguousLegacyCredentials(t *testing.T) {
	socks := &protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{Username: "typed-user", Password: "typed-password"}}
	https := &protocolconfig.Config{Version: 1, Type: "https", HTTP: &protocolconfig.HTTP{Username: "typed-user", Password: "typed-password"}, TLS: &protocolconfig.TLS{ServerName: "entry.example"}}
	for _, tc := range []struct {
		name   string
		config *protocolconfig.Config
		auth   Auth
		valid  bool
	}{
		{"socks-empty", socks, Auth{}, true},
		{"socks-identical", socks, Auth{Username: "typed-user", Password: "typed-password"}, true},
		{"socks-other-user", socks, Auth{Username: "other-user", Password: "typed-password"}, false},
		{"socks-other-password", socks, Auth{Username: "typed-user", Password: "other-password"}, false},
		{"socks-username-only", socks, Auth{Username: "typed-user"}, false},
		{"socks-password-only", socks, Auth{Password: "typed-password"}, false},
		{"socks-no-auth-with-legacy", &protocolconfig.Config{Version: 1, Type: "socks5", SOCKS5: &protocolconfig.SOCKS5{}}, Auth{Username: "user", Password: "password"}, false},
		{"https-empty", https, Auth{}, true},
		{"https-identical-still-ambiguous", https, Auth{Username: "typed-user", Password: "typed-password"}, false},
		{"https-username-only", https, Auth{Username: "typed-user"}, false},
		{"https-password-only", https, Auth{Password: "typed-password"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := Profile{Protocol: tc.config.Type, ProtocolConfig: tc.config, Auth: tc.auth}
			c, err := p.EffectiveProtocol()
			if tc.valid {
				if err != nil || c.Type != p.Protocol {
					t.Fatal("unambiguous configuration rejected")
				}
			} else if err != protocolconfig.ErrInvalidConfig || c != (protocolconfig.Config{}) {
				t.Fatal("ambiguous credentials accepted or included in failed result")
			}
		})
	}
}
