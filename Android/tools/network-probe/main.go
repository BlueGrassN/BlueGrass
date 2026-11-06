package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/netip"
	"os"
	"strings"
	"time"
)

const (
	stageTimeout = 8 * time.Second
	totalTimeout = 45 * time.Second
	ipifyURL     = "https://api.ipify.org?format=json"
	alternateURL = "https://ipv4.icanhazip.com"
)

type status struct {
	OK         bool   `json:"ok"`
	DurationMS int64  `json:"duration_ms"`
	Error      string `json:"error"`
}

type dnsResult struct {
	status
	Host   string `json:"host"`
	ACount int    `json:"a_count"`
}

type httpsAttempt struct {
	status
	URL        string `json:"url"`
	PublicIPv4 string `json:"public_ipv4"`
}

type httpsResult struct {
	status
	Endpoint   string         `json:"endpoint"`
	PublicIPv4 string         `json:"public_ipv4"`
	Attempts   []httpsAttempt `json:"attempts"`
}

type stunResult struct {
	status
	Server     string `json:"server"`
	PublicIPv4 string `json:"public_ipv4"`
	MappedPort uint16 `json:"mapped_port"`
	Attempts   int    `json:"attempts"`
}

type caResult struct {
	SystemPoolLoaded     bool   `json:"system_pool_loaded"`
	AndroidCAFilesLoaded int    `json:"android_ca_files_loaded"`
	Error                string `json:"error"`
}

type counts struct {
	Total  int `json:"total"`
	OK     int `json:"ok"`
	Failed int `json:"failed"`
}

type report struct {
	SchemaVersion int            `json:"schema_version"`
	DNSServer     string         `json:"dns_server"`
	UID           int            `json:"uid"`
	DurationMS    int64          `json:"duration_ms"`
	Error         string         `json:"error"`
	Counts        counts         `json:"counts"`
	CA            caResult       `json:"ca"`
	DNS           dnsResult      `json:"dns"`
	HTTPS         httpsResult    `json:"https"`
	STUN          []stunResult   `json:"stun"`
	Routing       *routingReport `json:"routing,omitempty"`
}

func initialReport() report {
	return report{
		SchemaVersion: 1, UID: os.Getuid(), Counts: counts{Total: 4, Failed: 4},
		CA:    caResult{Error: "not_run"},
		DNS:   dnsResult{status: status{Error: "not_run"}, Host: "example.com"},
		HTTPS: httpsResult{status: status{Error: "not_run"}, Attempts: []httpsAttempt{}},
		STUN: []stunResult{
			{status: status{Error: "not_run"}, Server: "stun.cloudflare.com:3478"},
			{status: status{Error: "not_run"}, Server: "stun.l.google.com:19302"},
		},
	}
}

type options struct {
	DNSServer    string
	RoutingCheck bool
	RoutingIPs   map[string]netip.Addr
}

func parseArgs(args []string) (options, bool) {
	var opts options
	opts.RoutingIPs = make(map[string]netip.Addr)
	flags := flag.NewFlagSet("network-probe", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	server := flags.String("dns-server", "", "IPv4 literal:53 (required)")
	flags.BoolVar(&opts.RoutingCheck, "routing-check", false, "include split-routing observations")
	flags.Func("routing-ip", "fixed endpoint domain=public IPv4 for the literal path (repeat per domain)", func(value string) error {
		host, literal, found := strings.Cut(value, "=")
		ip, err := netip.ParseAddr(literal)
		if !found || !routingHost(host) || err != nil || !isPublicIPv4(ip) || opts.RoutingIPs[host].IsValid() {
			return probeError("invalid_arguments")
		}
		opts.RoutingIPs[host] = ip
		return nil
	})
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return options{}, false
	}
	addr, err := netip.ParseAddrPort(*server)
	if err != nil || !addr.Addr().Is4() || addr.Port() != 53 || addr.Addr().IsUnspecified() || addr.Addr().IsMulticast() || addr.Addr().String() == "255.255.255.255" {
		return options{}, false
	}
	if !opts.RoutingCheck && len(opts.RoutingIPs) != 0 {
		return options{}, false
	}
	opts.DNSServer = addr.String()
	return opts, true
}

func run(args []string) (report, int) {
	start := time.Now()
	r := initialReport()
	opts, valid := parseArgs(args)
	if !valid {
		r.Error = "invalid_arguments"
		r.DurationMS = time.Since(start).Milliseconds()
		return r, 2
	}
	r.DNSServer = opts.DNSServer
	ctx, cancel := context.WithTimeout(context.Background(), totalTimeout)
	defer cancel()
	resolver := explicitResolver{server: opts.DNSServer}
	r.DNS = probeDNS(ctx, resolver)
	pool, trust := loadTrust(ctx)
	r.CA = trust
	var routingReady chan routingReport
	if opts.RoutingCheck {
		routingReady = make(chan routingReport, 1)
		go func() { routingReady <- probeRouting(ctx, resolver, pool, opts.RoutingIPs) }()
	}
	r.HTTPS = probeHTTPS(ctx, resolver, pool)
	for i := range r.STUN {
		r.STUN[i] = probeSTUN(ctx, resolver, r.STUN[i].Server)
	}
	for _, ok := range []bool{r.DNS.OK, r.HTTPS.OK, r.STUN[0].OK, r.STUN[1].OK} {
		if ok {
			r.Counts.OK++
		}
	}
	r.Counts.Failed = r.Counts.Total - r.Counts.OK
	if routingReady != nil {
		routing := <-routingReady
		r.Routing = &routing
	}
	r.DurationMS = time.Since(start).Milliseconds()
	if ctx.Err() != nil {
		r.Error = "deadline_exceeded"
	} else if r.Counts.Failed > 0 || (r.Routing != nil && r.Routing.Counts.Failed > 0) {
		r.Error = "probe_failed"
	}
	if r.Error != "" {
		return r, 1
	}
	return r, 0
}

func main() {
	r, code := run(os.Args[1:])
	// Exactly one JSON line; neither argument parsing nor network errors print.
	if json.NewEncoder(os.Stdout).Encode(r) != nil {
		os.Exit(1)
	}
	os.Exit(code)
}
