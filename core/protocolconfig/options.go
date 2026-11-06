package protocolconfig

import (
	"encoding/json"
	"net/netip"

	"bluegrass/native/core/gate"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/infra/conf"
)

// Build binds one validated resource to one resolved public IPv4 entry. Only
// fields from the closed wire contract become Xray settings; resources cannot
// inject routing, listeners, paths, TLS trust overrides or socket options.
func Build(c Config, entry netip.AddrPort) (*conf.OutboundDetourConfig, error) {
	if c.Validate() != nil || !entry.IsValid() || !entry.Addr().Is4() || !gate.PublicIPv4(entry.Addr().String()) || entry.Port() == 0 {
		return nil, ErrInvalidConfig
	}
	if err := c.Supported(); err != nil {
		return nil, err
	}
	address := &conf.Address{Address: xnet.ParseAddress(entry.Addr().String())}
	network := conf.TransportProtocol("tcp")
	stream := &conf.StreamConfig{Network: &network}
	if c.TLS != nil {
		tls := &conf.TLSConfig{ServerName: c.TLS.ServerName, MinVersion: "1.2"}
		alpn := conf.StringList(append([]string(nil), c.TLS.ALPN...))
		if c.Type == "hysteria2" || c.Type == "vless" && c.VLESS.Flow == "xtls-rprx-vision" {
			tls.MinVersion = "1.3"
		}
		if len(alpn) == 0 {
			switch c.Type {
			case "https":
				alpn = conf.StringList{"http/1.1"}
			case "hysteria2":
				alpn = conf.StringList{"h3"}
			}
		}
		if len(alpn) != 0 {
			tls.ALPN = &alpn
		}
		stream.Security, stream.TLSSettings = "tls", tls
	}
	o := &conf.OutboundDetourConfig{Protocol: c.Type, Tag: "selected", StreamSetting: stream}
	var settings any
	switch c.Type {
	case "socks5":
		o.Protocol = "socks"
		settings = &conf.SocksClientConfig{Address: address, Port: entry.Port(), Username: c.SOCKS5.Username, Password: c.SOCKS5.Password}
	case "shadowsocks":
		settings = &conf.ShadowsocksClientConfig{Address: address, Port: entry.Port(), Cipher: c.Shadowsocks.Method, Password: c.Shadowsocks.Password}
	case "vmess":
		settings = &conf.VMessOutboundConfig{Address: address, Port: entry.Port(), ID: c.VMess.UUID, Security: c.VMess.Security}
	case "vless":
		settings = &conf.VLessOutboundConfig{Address: address, Port: entry.Port(), Id: c.VLESS.UUID, Flow: c.VLESS.Flow, Encryption: "none"}
	case "trojan":
		settings = &conf.TrojanClientConfig{Address: address, Port: entry.Port(), Password: c.Trojan.Password}
	case "http", "https":
		o.Protocol = "http"
		settings = &conf.HTTPClientConfig{Address: address, Port: entry.Port(), Username: c.HTTP.Username, Password: c.HTTP.Password}
	case "hysteria2":
		o.Protocol, network = "hysteria", "hysteria"
		settings = &conf.HysteriaClientConfig{Version: 2, Address: address, Port: entry.Port()}
		stream.HysteriaSettings = &conf.HysteriaConfig{Version: 2, Auth: c.Hysteria2.Password}
		if c.Hysteria2.Obfs != nil {
			raw, _ := json.Marshal(&conf.Salamander{Password: c.Hysteria2.Obfs.Password})
			mask := json.RawMessage(raw)
			stream.FinalMask = &conf.FinalMask{Udp: []conf.Mask{{Type: "salamander", Settings: &mask}}}
		}
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	raw := json.RawMessage(encoded)
	o.Settings = &raw
	return o, nil
}
