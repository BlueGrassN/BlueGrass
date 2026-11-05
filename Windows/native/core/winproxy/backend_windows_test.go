//go:build windows

package winproxy

import (
	"errors"
	"reflect"
	"syscall"
	"testing"
	"unsafe"
)

func putOptionString(o *connectionOption, p *uint16) { *(**uint16)(unsafe.Pointer(&o.Value)) = p }
func optionString(o *connectionOption) string {
	p := *(**uint16)(unsafe.Pointer(&o.Value))
	if p == nil {
		return ""
	}
	var out []uint16
	for i := 0; i < 4096; i++ {
		v := *(*uint16)(unsafe.Add(unsafe.Pointer(p), i*2))
		if v == 0 {
			break
		}
		out = append(out, v)
	}
	return syscall.UTF16ToString(out)
}

func TestWinInetABI(t *testing.T) {
	wantOption, wantList, wantValue := uintptr(16), uintptr(32), uintptr(8)
	if unsafe.Sizeof(uintptr(0)) == 4 {
		wantOption, wantList, wantValue = 12, 20, 4
	}
	if unsafe.Sizeof(connectionOption{}) != wantOption || unsafe.Sizeof(connectionOptions{}) != wantList || unsafe.Offsetof(connectionOption{}.Value) != wantValue {
		t.Fatal("incorrect WinInet ABI")
	}
}

func TestWinInetQueryReadsAllFourFieldsAndFreesStrings(t *testing.T) {
	values := []string{"http=旧代理.invalid:8080;https=old.invalid:8080", "<local>;*.内网.invalid", "https://old.invalid/配置.pac"}
	var allocations [][]uint16
	freed := 0
	b := &winInetBackend{
		query: func(list *connectionOptions) error {
			if list.Size != uint32(unsafe.Sizeof(*list)) || list.Connection != nil || list.Count != 4 {
				t.Fatal("wrong query scope/size")
			}
			options := unsafe.Slice(list.Options, 4)
			if options[0].Option != 10 || options[1].Option != 2 || options[2].Option != 3 || options[3].Option != 4 {
				t.Fatal("incomplete query")
			}
			options[0].Value = 15
			for i, value := range values {
				v, _ := syscall.UTF16FromString(value)
				allocations = append(allocations, v)
				putOptionString(&options[i+1], &v[0])
			}
			return nil
		},
		free: func(*uint16) { freed++ },
	}
	got, err := b.Query()
	want := Settings{Flags: 15, Server: values[0], Bypass: values[1], AutoConfigURL: values[2]}
	if err != nil || got != want || freed != 3 {
		t.Fatalf("query=%+v err=%v frees=%d", got, err, freed)
	}
	_ = allocations
}

func TestWinInetLegacyFallbackAndFailureCleanup(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "error"}[fail], func(t *testing.T) {
			calls, freed := 0, 0
			v, _ := syscall.UTF16FromString("allocated.invalid")
			b := &winInetBackend{free: func(*uint16) { freed++ }}
			b.query = func(list *connectionOptions) error {
				calls++
				options := unsafe.Slice(list.Options, 4)
				putOptionString(&options[1], &v[0])
				if calls == 1 {
					return syscall.Errno(87)
				}
				if options[0].Option != 1 {
					t.Fatal("fallback did not request FLAGS")
				}
				if fail {
					return errInjected
				}
				options[0].Value = 9
				return nil
			}
			got, err := b.Query()
			if calls != 2 || freed != 2 {
				t.Fatalf("calls=%d frees=%d", calls, freed)
			}
			if fail {
				if !errors.Is(err, errInjected) {
					t.Fatal(err)
				}
			} else if err != nil || got.Flags != 9 {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestWinInetSetWritesCompleteSnapshotWithoutAutomaticNotification(t *testing.T) {
	b := &winInetBackend{}
	called := false
	b.set = func(list *connectionOptions) error {
		called = true
		if list.Count != 4 || list.Connection != nil {
			t.Fatal("wrong set scope")
		}
		o := unsafe.Slice(list.Options, 4)
		if o[0].Option != 1 || o[0].Value != 15 || o[1].Option != 2 || o[2].Option != 3 || o[3].Option != 4 {
			t.Fatal("wrong option flags")
		}
		if optionString(&o[1]) != "old.invalid:3128" || optionString(&o[2]) != "<local>;*.old.invalid" || optionString(&o[3]) != "https://old.invalid/pac" {
			t.Fatal("lost snapshot field")
		}
		return nil
	}
	if err := b.Set(originalSettings()); err != nil || !called {
		t.Fatalf("set=%v called=%v", err, called)
	}
}

func TestWinInetNotifyAttemptsBothAndPropagatesErrors(t *testing.T) {
	for _, failAt := range []uint32{0, 39, 37} {
		var calls []uint32
		b := &winInetBackend{notify: func(option uint32) error {
			calls = append(calls, option)
			if option == failAt {
				return errInjected
			}
			return nil
		}}
		err := b.Notify()
		if !reflect.DeepEqual(calls, []uint32{39, 37}) {
			t.Fatalf("notifications=%v", calls)
		}
		if failAt == 0 && err != nil || failAt != 0 && !errors.Is(err, errInjected) {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestWinInetInvalidStringNeverCallsSet(t *testing.T) {
	b := &winInetBackend{set: func(*connectionOptions) error { t.Fatal("invalid settings reached API"); return nil }}
	s := originalSettings()
	s.Bypass = "bad\x00data"
	if err := b.Set(s); !errors.Is(err, ErrInvalidSettings) {
		t.Fatal(err)
	}
}
