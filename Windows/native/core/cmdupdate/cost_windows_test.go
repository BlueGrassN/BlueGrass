//go:build windows

package cmdupdate

import (
	"context"
	"net"
	"net/netip"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestNLMReaderDeadlineFailsUnknownWithoutUnboundedWorkers(t *testing.T) {
	// No worker consumes requests: models a blocked COM call without invoking COM.
	r := &nlmReader{requests: make(chan nlmRequest), stop: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	if result, err := r.Read(ctx, adapterFixture); err == nil || result.Flags != 0 {
		t.Fatal("blocked provider returned usable cost", err)
	}
	r.Close()
	if _, err := r.Read(context.Background(), adapterFixture); err == nil {
		t.Fatal("closed NLM provider accepted query")
	}
}

func TestReadOnlyDestinationRouteAndNLMCostSmoke(t *testing.T) {
	if os.Getenv("BLUEGRASS_NLM_SMOKE") != "1" {
		t.Skip("opt-in physical route/NLM probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// This reads destination-specific routing metadata. It does not dial this IP
	// or resolve a release host; no configured feed/public key is needed or added.
	binding, err := selectPhysicalBinding(ctx, netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		t.Skip("verified physical route unavailable; no fallback", err)
	}
	r := newNLMReader()
	defer r.Close()
	var sample connectionCost
	for attempt := 0; attempt < 3; attempt++ {
		sample, err = r.Read(ctx, binding.adapterGUID)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Skip("exact destination route has unknown NLM cost; consent required", err)
	}
	if err := binding.verify(ctx); err != nil {
		t.Fatal("physical route changed during NLM probe", err)
	}
	t.Logf("verified destination-specific physical route and exact NLM adapter match; flags=%#x classification=%d; no dial/DNS/configuration writes", sample.Flags, classifyNLMCost(sample.Flags))
}

func TestReadOnlyNLMWindowsSmoke(t *testing.T) {
	if os.Getenv("BLUEGRASS_NLM_SMOKE") != "1" {
		t.Skip("opt-in read-only NLM probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r := newNLMReader()
	defer r.Close()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal("read-only interface enumeration failed", err)
	}
	matched := 0
	for _, networkInterface := range interfaces {
		row := windows.MibIfRow2{InterfaceIndex: uint32(networkInterface.Index)}
		if windows.GetIfEntry2Ex(windows.MibIfEntryNormalWithoutStatistics, &row) != nil || row.InterfaceAndOperStatusFlags&1 == 0 || row.Type != 6 && row.Type != 71 && row.Type != 243 && row.Type != 244 || row.OperStatus != windows.IfOperStatusUp {
			continue
		}
		var sample connectionCost
		for attempt := 0; attempt < 3; attempt++ {
			sample, err = r.Read(ctx, row.InterfaceGuid.String())
			if err == nil {
				break
			}
		}
		if err != nil {
			continue
		}
		matched++
		t.Logf("matched physical adapter by GUID; NLM flags=%#x classification=%d; no socket/DNS/route changes", sample.Flags, classifyNLMCost(sample.Flags))
	}
	if matched == 0 {
		t.Skip("no connected hardware adapter had a usable exact NLM match; runtime must retain unknown-cost consent")
	}
}
