package bridge

const validConfig = `{"version":1,"host":"entry.example.invalid","port":1080,"protocolConfig":{"version":1,"type":"socks5","socks5":{"username":"fixture","password":"FAKE_SECRET"}},"expiresAtUtc":null,"effectiveNowUtc":"2026-10-13T08:00:00Z"}`

var fixtureNetwork = networkInfo{Name: "wlan0", Index: 7, MTU: 1500, Addresses: []string{"192.168.8.5/24", "fd00:1234::2/64"}, DNSServers: []string{"192.168.8.1"}, Domains: []string{"home.arpa"}, Routes: []string{"192.168.8.0/24"}}
