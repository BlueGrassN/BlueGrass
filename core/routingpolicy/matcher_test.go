package routingpolicy

import (
	"net/netip"
	"sync"
	"testing"
)

func TestBundledDomainRules(t *testing.T) {
	r, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		host string
		want Decision
	}{
		{"baidu.com", Direct}, {"www.baidu.com", Direct}, {"WWW.BAIDU.COM.", Direct},
		{"qq.com", Direct}, {"bilibili.com", Direct}, {"jd.com", Direct},
		{"google.com", Proxy}, {"www.google.com", Proxy}, {"youtube.com", Proxy}, {"github.com", Proxy},
		{"baidu.com.evil.invalid", Unknown}, {"notbaidu.com", Unknown},
		{"google.com.evil.invalid", Unknown}, {"notgoogle.com", Unknown},
		{"unlisted-routing-test.invalid", Unknown}, {"", Unknown}, {".", Unknown},
		{"baidu.com..", Unknown}, {".baidu.com", Unknown}, {"a..baidu.com", Unknown},
		{"https://baidu.com", Unknown}, {"baidu.com:443", Unknown}, {"baidu.com\x00.evil.invalid", Unknown},
		{" baidu.com", Unknown}, {"223.5.5.5", Unknown}, {"::ffff:223.5.5.5", Unknown},
	} {
		t.Run(tc.host, func(t *testing.T) {
			if got := r.Domain(tc.host); got != tc.want {
				t.Errorf("Domain(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestCNIPv4AndSpecialRanges(t *testing.T) {
	r, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	// Loyalsoldier's CN list includes the DNSPod range omitted by the old data.
	for _, ip := range []string{"223.5.5.5", "119.29.28.1", "119.29.29.29", "114.114.114.114", "180.76.76.76"} {
		if !r.IsCN(netip.MustParseAddr(ip)) {
			t.Errorf("IsCN(%s) = false", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "9.9.9.9", "208.67.222.222"} {
		if r.IsCN(netip.MustParseAddr(ip)) {
			t.Errorf("IsCN(%s) = true", ip)
		}
	}
	for _, ip := range specialIPs() {
		if r.IsCN(ip) || r.ShouldDirect("baidu.com", ip) {
			t.Errorf("special IP %s allowed", ip)
		}
	}
}

func specialIPs() []netip.Addr {
	result := []netip.Addr{{}}
	for _, s := range []string{
		"0.0.0.0", "0.1.2.3", "10.0.0.1", "100.64.0.1", "100.127.255.255", "127.0.0.1",
		"169.254.1.1", "172.16.0.1", "192.168.1.1", "192.0.0.1", "192.0.2.1", "198.18.0.1",
		"198.19.255.255", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "fc00::1", "fe80::1%12", "2400:3200::1", "2001:4860:4860::8888", "::ffff:223.5.5.5",
	} {
		result = append(result, netip.MustParseAddr(s))
	}
	return result
}

func TestShouldDirectPriority(t *testing.T) {
	r, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		domain, ip string
		want       bool
	}{
		{"baidu.com", "8.8.8.8", true}, {"baidu.com", "223.5.5.5", true},
		{"google.com", "223.5.5.5", false}, {"google.com", "8.8.8.8", false},
		{"google.com", "119.29.29.29", false}, {"", "119.29.29.29", true},
		{"unlisted-routing-test.invalid", "223.5.5.5", true}, {"unlisted-routing-test.invalid", "8.8.8.8", false},
		{"", "223.5.5.5", true}, {"", "8.8.8.8", false},
	} {
		if got := r.ShouldDirect(tc.domain, netip.MustParseAddr(tc.ip)); got != tc.want {
			t.Errorf("ShouldDirect(%q, %s) = %v, want %v", tc.domain, tc.ip, got, tc.want)
		}
	}
}

func TestLoadSingletonConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	loaded := make(chan *Rules, 32)
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := Load()
			if err != nil {
				t.Error(err)
				return
			}
			loaded <- r
			for range 50 {
				if r.Domain("www.baidu.com") != Direct || r.Domain("www.google.com") != Proxy || !r.IsCN(netip.MustParseAddr("223.5.5.5")) {
					t.Error("inconsistent concurrent lookup")
					return
				}
			}
		}()
	}
	wg.Wait()
	close(loaded)
	var first *Rules
	for r := range loaded {
		if first == nil {
			first = r
		}
		if r != first {
			t.Fatal("Load returned different instances")
		}
	}
}
