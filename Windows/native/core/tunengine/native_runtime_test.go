package tunengine

import (
	"context"
	"errors"
	tun "github.com/xtls/xray-core/proxy/tun"
	"net/netip"
	"runtime"
	"testing"
)

type fakeNativeDevice struct {
	tun.Tun
	starts, closes     int
	startErr, closeErr error
	onStart            func()
}

func TestNativeDNSUsesUnassignedOnLinkPeer(t *testing.T) {
	for _, pool := range []string{"172.31.255.1/30", "172.30.255.1/30", "10.255.255.1/30"} {
		v4 := netip.MustParsePrefix(pool)
		options := nativeOptions(v4, netip.MustParsePrefix("fdfe:dcba:9876::1/126"))
		if len(options.DNS) != 1 {
			t.Fatal("expected one owned DNS destination")
		}
		dns := netip.MustParseAddr(options.DNS[0])
		if !v4.Contains(dns) || dns == v4.Masked().Addr() || !v4.Contains(dns.Next()) {
			t.Fatal("DNS destination must be a usable address on the owned link")
		}
		for _, assigned := range options.Gateway {
			if runtime.GOOS != "darwin" && netip.MustParsePrefix(assigned).Addr() == dns {
				t.Fatal("Windows delivers DNS to an assigned local address instead of the TUN stack")
			}
		}
	}
}

func (d *fakeNativeDevice) Start() error {
	d.starts++
	if d.onStart != nil {
		d.onStart()
	}
	return d.startErr
}
func (d *fakeNativeDevice) Close() error { d.closes++; return d.closeErr }

type fakeNativeStack struct {
	tun.Stack
	starts, closes     int
	startErr, closeErr error
	onStart            func()
}

func (s *fakeNativeStack) Start() error {
	s.starts++
	if s.onStart != nil {
		s.onStart()
	}
	return s.startErr
}
func (s *fakeNativeStack) Close() error { s.closes++; return s.closeErr }
func TestNativeActivationRollbackAndClosedGate(t *testing.T) {
	for _, failure := range []string{"open", "stack-create", "stack-start", "device-start", "cancel", ""} {
		t.Run(failure, func(t *testing.T) {
			g := newSessionGate()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			device := &fakeNativeDevice{}
			stack := &fakeNativeStack{}
			platform := &nativePlatform{}
			platform.open = func(options *tun.Config) (tun.Tun, uint64, error) {
				if g.isOpen() || len(options.AutoSystemWfpBlockLeak) != 0 || options.AutoOutboundsInterface != "" || options.AutoSystemDnsToGateway {
					t.Fatal("unsafe activation")
				}
				if runtime.GOOS == "darwin" {
					if len(options.AutoSystemRoutingTable) != 0 {
						t.Fatal("Darwin manager owns system routes")
					}
				} else if len(options.AutoSystemRoutingTable) == 0 {
					t.Fatal("missing Xray-owned system routes")
				}
				if failure == "open" {
					return nil, 0, errors.New("fixture")
				}
				return device, 42, nil
			}
			platform.makeStack = func(_ context.Context, options tun.StackOptions, handler tun.ConnectionHandler) (tun.Stack, error) {
				if g.isOpen() || options.Tun != device || handler == nil {
					t.Fatal("unsafe stack")
				}
				if failure == "stack-create" {
					return nil, errors.New("fixture")
				}
				if failure == "stack-start" {
					stack.startErr = errors.New("fixture")
				}
				if failure == "device-start" {
					device.startErr = errors.New("fixture")
				}
				if failure == "cancel" {
					cancel()
				}
				return stack, nil
			}
			d, s, e := startNative(ctx, platform, nativeOptions(netip.MustParsePrefix("172.31.255.1/30"), netip.MustParsePrefix("fdfe:dcba:9876::1/126")), nil, g)
			if failure == "" {
				if e != nil || d != device || s != stack || platform.luid.Load() != 42 || g.isOpen() {
					t.Fatal("wrong successful activation")
				}
				_ = d.Close()
				_ = s.Close()
				return
			}
			if e == nil || d != nil || s != nil || platform.luid.Load() != 0 {
				t.Fatal("failed activation survived", e)
			}
			if failure != "open" && device.closes != 1 {
				t.Fatal("device not rolled back")
			}
			if failure == "cancel" && (stack.starts != 0 || device.starts != 0) {
				t.Fatal("cancellation during makeStack reached Start and modified system configuration")
			}
		})
	}
}

func TestNativeActivationChecksCancellationAndRetirementBetweenStages(t *testing.T) {
	for _, cause := range []string{"cancel", "retire"} {
		for _, stage := range []string{"before-open", "open", "make-stack", "stack-start", "device-start"} {
			t.Run(cause+"/"+stage, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				g := newSessionGate()
				defer g.retire()
				stop := func() {
					if cause == "cancel" {
						cancel()
					} else {
						g.retire()
					}
				}
				device, stack := &fakeNativeDevice{}, &fakeNativeStack{}
				opens, makes := 0, 0
				p := &nativePlatform{
					open: func(*tun.Config) (tun.Tun, uint64, error) {
						opens++
						if stage == "open" {
							stop()
						}
						return device, 42, nil
					},
					makeStack: func(context.Context, tun.StackOptions, tun.ConnectionHandler) (tun.Stack, error) {
						makes++
						if stage == "make-stack" {
							stop()
						}
						return stack, nil
					},
				}
				if stage == "before-open" {
					stop()
				}
				if stage == "stack-start" {
					stack.onStart = stop
				}
				if stage == "device-start" {
					device.onStart = stop
				}
				d, s, err := startNative(ctx, p, &tun.Config{}, nil, g)
				if err == nil || d != nil || s != nil || p.luid.Load() != 0 {
					t.Fatalf("stopped activation survived: %v", err)
				}
				wantOpens, wantMakes, wantStackStarts, wantDeviceStarts := 1, 1, 1, 1
				switch stage {
				case "before-open":
					wantOpens, wantMakes, wantStackStarts, wantDeviceStarts = 0, 0, 0, 0
				case "open":
					wantMakes, wantStackStarts, wantDeviceStarts = 0, 0, 0
				case "make-stack":
					wantStackStarts, wantDeviceStarts = 0, 0
				case "stack-start":
					wantDeviceStarts = 0
				}
				if opens != wantOpens || makes != wantMakes || stack.starts != wantStackStarts || device.starts != wantDeviceStarts {
					t.Fatalf("stages = %d/%d/%d/%d, want %d/%d/%d/%d", opens, makes, stack.starts, device.starts, wantOpens, wantMakes, wantStackStarts, wantDeviceStarts)
				}
				if device.closes != wantOpens || stack.closes != wantMakes {
					t.Fatalf("cleanup = %d/%d, want %d/%d", device.closes, stack.closes, wantOpens, wantMakes)
				}
			})
		}
	}
}
func TestNativeFailedRollbackRetainsCleanupHandleAndLUID(t *testing.T) {
	device := &fakeNativeDevice{startErr: errors.New("start"), closeErr: errors.New("cleanup")}
	stack := &fakeNativeStack{}
	platform := &nativePlatform{open: func(*tun.Config) (tun.Tun, uint64, error) { return device, 42, nil }, makeStack: func(context.Context, tun.StackOptions, tun.ConnectionHandler) (tun.Stack, error) { return stack, nil }}
	d, s, e := startNative(context.Background(), platform, &tun.Config{}, nil, newSessionGate())
	if e == nil || d != device || s != stack || platform.luid.Load() != 42 {
		t.Fatal("unconfirmed cleanup hidden")
	}
}

func TestNativeOpenFailureRetainsUnconfirmedCleanup(t *testing.T) {
	device := &fakeNativeDevice{closeErr: errors.New("ownership unconfirmed")}
	p := &nativePlatform{open: func(*tun.Config) (tun.Tun, uint64, error) {
		return nil, 0, &nativeOpenError{err: errors.New("identity lookup failed"), device: device, luid: 42}
	}}
	d, s, err := startNative(context.Background(), p, &tun.Config{}, nil, newSessionGate())
	if err == nil || d != device || s != nil || p.luid.Load() != 42 {
		t.Fatal("failed opener hid a live resource or cleanup failure")
	}
}
func TestPhysicalEntryRouteUsesLongestPrefixMetricAndRejectsAmbiguity(t *testing.T) {
	adapters := []systemAdapter{{Name: "en0", LUID: 1, Up: true, Metric4: 20}, {Name: "en1", LUID: 2, Up: true, Metric4: 1}, {Name: InterfaceName, LUID: 3, Up: true}}
	routes := []systemRoute{{LUID: 1, Prefix: netip.MustParsePrefix("8.8.8.0/24"), Metric: 10}, {LUID: 2, Prefix: netip.MustParsePrefix("0.0.0.0/0")}, {LUID: 3, Prefix: netip.MustParsePrefix("8.8.8.8/32")}}
	entry := netip.MustParseAddr("8.8.8.8")
	name, e := selectPhysicalInterface(entry, adapters, routes)
	if e != nil || name != "en0" {
		t.Fatal(name, e)
	}
	routes = append(routes, systemRoute{LUID: 2, Prefix: netip.MustParsePrefix("8.8.8.0/24"), Metric: 29})
	if _, e = selectPhysicalInterface(entry, adapters, routes); e == nil {
		t.Fatal("ambiguous entry route accepted")
	}
	if _, e = selectPhysicalInterface(entry, adapters, routes[2:3]); e == nil {
		t.Fatal("selected TUN as physical egress")
	}
}
