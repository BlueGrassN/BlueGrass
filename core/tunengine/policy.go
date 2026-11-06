package tunengine

import (
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"slices"

	"github.com/miekg/dns"
	M "github.com/sagernet/sing/common/metadata"
)

// The service builds this closed policy schema. It is never accepted over IPC.
// Rules are evaluated here before any socket reaches Xray or a physical NIC.
type nativeRule struct {
	Action      string         `json:"action"`
	Outbound    string         `json:"outbound"`
	Server      string         `json:"server"`
	Port        []uint16       `json:"port"`
	Network     string         `json:"network"`
	IPVersion   int            `json:"ip_version"`
	IPCIDR      []netip.Prefix `json:"ip_cidr"`
	ProcessPath []string       `json:"process_path"`
	DomainRegex []string       `json:"domain_regex"`
	QueryType   []string       `json:"query_type"`
	Rcode       string         `json:"rcode"`
	Answer      []string       `json:"answer"`
	patterns    []*regexp.Regexp
}
type nativeOutbound struct {
	Type             string `json:"type"`
	Tag              string `json:"tag"`
	BindInterface    string `json:"bind_interface"`
	Inet4BindAddress string `json:"inet4_bind_address"`
	Inet6BindAddress string `json:"inet6_bind_address"`
}
type nativeServer struct {
	Type, Tag, Server string
	ServerPort        uint16   `json:"server_port"`
	BindInterface     string   `json:"bind_interface"`
	Interface         []string `json:"interface"`
}
type nativeConfig struct {
	Outbounds []nativeOutbound
	Route     struct {
		Rules               []nativeRule
		Final               string
		AutoDetectInterface bool `json:"auto_detect_interface"`
	}
	DNS struct {
		Rules   []nativeRule
		Servers []nativeServer
		Final   string
	}
}
type inboundContext struct {
	Network             string
	Source, Destination M.Socksaddr
	ProcessPath         string
	domestic            bool // trusted classification, never an ingress/API field
}

func decodePolicy(raw obj) (nativeConfig, error) {
	var c nativeConfig
	data, err := json.Marshal(raw)
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	for i := range c.DNS.Rules {
		for _, pattern := range c.DNS.Rules[i].DomainRegex {
			r, e := regexp.Compile(pattern)
			if e != nil {
				return c, errors.New("INVALID_DNS_POLICY")
			}
			c.DNS.Rules[i].patterns = append(c.DNS.Rules[i].patterns, r)
		}
	}
	return c, nil
}
func (c nativeConfig) route(m inboundContext) string {
	for _, r := range c.Route.Rules {
		if r.Action == "resolve" {
			continue
		}
		if len(r.Port) > 0 && !slices.Contains(r.Port, m.Destination.Port) {
			continue
		}
		if r.Network != "" && r.Network != m.Network {
			continue
		}
		if r.IPVersion == 4 && !m.Destination.Addr.Is4() || r.IPVersion == 6 && !m.Destination.Addr.Is6() {
			continue
		}
		if len(r.ProcessPath) > 0 && !slices.Contains(r.ProcessPath, m.ProcessPath) {
			continue
		}
		if len(r.IPCIDR) > 0 {
			match := false
			for _, prefix := range r.IPCIDR {
				if prefix.Contains(m.Destination.Addr.WithZone("")) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		if r.Outbound != "" {
			return r.Outbound
		}
		return r.Action
	}
	if c.Route.Final != "" {
		return c.Route.Final
	}
	return "reject"
}
func (c nativeConfig) dnsRule(name string, qt uint16) nativeRule {
	for _, r := range c.DNS.Rules {
		if len(r.QueryType) > 0 && !slices.Contains(r.QueryType, dns.TypeToString[qt]) {
			continue
		}
		if len(r.patterns) > 0 {
			match := false
			for _, p := range r.patterns {
				if p.MatchString(name) {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		return r
	}
	return nativeRule{Server: c.DNS.Final}
}
