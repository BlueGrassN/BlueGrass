//go:build windows && (amd64 || arm64)

package entrydns

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

type testRawConn struct {
	fd   uintptr
	fail error
}

func (c testRawConn) Control(f func(uintptr)) error {
	if c.fail != nil {
		return c.fail
	}
	f(c.fd)
	return nil
}
func (testRawConn) Read(func(uintptr) bool) error  { return errors.New("unexpected read") }
func (testRawConn) Write(func(uintptr) bool) error { return errors.New("unexpected write") }

func TestPhysicalSocketBindingUsesFamilySpecificByteOrderAndRejectsErrors(t *testing.T) {
	for _, tc := range []struct {
		dest, local  string
		level, value int
	}{
		{"1.1.1.1", "192.168.1.20", 0, 0x44332211},
		{"2606:4700:4700::1111", "2001:db8::20", 41, 0x11223344},
	} {
		r := PhysicalRoute{Destination: netip.MustParseAddr(tc.dest), LocalAddress: netip.MustParseAddr(tc.local), InterfaceIndex: 0x11223344}
		denied := errors.New("socket option rejected")
		calls := 0
		set := func(fd uintptr, level, option, value int) error {
			calls++
			if fd != 99 || level != tc.level || option != 31 || value != tc.value {
				t.Fatalf("wrong unicast interface encoding: fd=%d level=%d option=%d value=%x", fd, level, option, value)
			}
			return denied
		}
		if !errors.Is(bindPhysicalInterface(testRawConn{fd: 99}, r, set), denied) || calls != 1 {
			t.Fatal("setsockopt error was ignored")
		}
		if !errors.Is(bindPhysicalInterface(testRawConn{fail: denied}, r, set), denied) || calls != 1 {
			t.Fatal("raw socket failure retried unbound")
		}
	}
}

func TestWinsockInterfaceReadbackUsesHostOrderForBothFamilies(t *testing.T) {
	if !interfaceOptionMatches(windows.IPPROTO_IP, 0x0b000000, 11) || interfaceOptionMatches(windows.IPPROTO_IP, 0x0b000000, 0x0b000000) {
		t.Fatal("IPv4 getter must decode the setter's network order")
	}
	if !interfaceOptionMatches(windows.IPPROTO_IPV6, 11, 11) || interfaceOptionMatches(windows.IPPROTO_IPV6, 11, 12) {
		t.Fatal("IPv6 getter/setter must both use host order")
	}
}

// Explicit opt-in. Reads interface/routes and tests options on unconnected
// sockets only: no DNS query, packet send, route write, WFP or service mutation.
func TestPhysicalRouteWindowsReadOnlySmoke(t *testing.T) {
	if os.Getenv("BLUEGRASS_PHYSICAL_SMOKE") != "1" {
		t.Skip("read-only interface smoke requires explicit opt-in")
	}
	if unsafe.Sizeof(physicalRouteRow{}) != 104 || unsafe.Offsetof(physicalRouteRow{}.NextHop) != 44 {
		t.Fatal("Win64 route ABI mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	route, err := SelectPhysicalRoute(ctx, netip.MustParseAddr("1.1.1.1"))
	if err != nil {
		t.Fatal("no verified physical IPv4 route", err)
	}
	if err := VerifyPhysicalRoute(ctx, route); err != nil {
		t.Fatal(err)
	}
	t.Logf("verified physical interface index=%d LUID=%d GUID=%s source=%s prefix=%s metric=%d", route.InterfaceIndex, route.InterfaceLUID, route.AdapterGUID, route.LocalAddress, route.Prefix, route.Metric)
	var data windows.WSAData
	if err := windows.WSAStartup(0x202, &data); err != nil {
		t.Fatal(err)
	}
	defer windows.WSACleanup()
	for _, kind := range []int{windows.SOCK_STREAM, windows.SOCK_DGRAM} {
		fd, err := windows.Socket(windows.AF_INET, kind, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = bindPhysicalInterface(testRawConn{fd: uintptr(fd)}, route, setVerifiedInterface)
		closeErr := windows.Closesocket(fd)
		if err != nil || closeErr != nil {
			t.Fatal("unconnected socket binding/readback failed", err, closeErr)
		}
	}
	v6, err := SelectPhysicalRoute(ctx, netip.MustParseAddr("2606:4700:4700::1111"))
	if err != nil {
		t.Log("No verified physical IPv6 route; IPv6 live socket check skipped (fails closed)")
		return
	}
	for _, kind := range []int{windows.SOCK_STREAM, windows.SOCK_DGRAM} {
		fd, err := windows.Socket(windows.AF_INET6, kind, 0)
		if err != nil {
			t.Fatal(err)
		}
		err = bindPhysicalInterface(testRawConn{fd: uintptr(fd)}, v6, setVerifiedInterface)
		closeErr := windows.Closesocket(fd)
		if err != nil || closeErr != nil {
			t.Fatal(err, closeErr)
		}
	}
}
