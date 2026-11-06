//go:build windows

package winproxy

import (
	"bytes"
	"errors"
	"testing"
	"unsafe"
)

func TestDPAPIUsesCurrentUserWithoutUIAndCopiesBeforeFree(t *testing.T) {
	input := []byte("sensitive-backup")
	output := []byte("protected-value")
	freed := false
	result, err := cryptData(input, func(in *dataBlob, flags uint32, out *dataBlob) error {
		if flags != 1 {
			t.Fatalf("flags=%d; must exclude CRYPTPROTECT_LOCAL_MACHINE", flags)
		}
		if !bytes.Equal(unsafe.Slice(in.Data, in.Size), input) {
			t.Fatal("lost input")
		}
		out.Size = uint32(len(output))
		out.Data = &output[0]
		return nil
	}, func(*byte) { freed = true; clear(output) })
	if err != nil || !freed || string(result) != "protected-value" {
		t.Fatalf("result=%q err=%v freed=%v", result, err, freed)
	}
}

func TestDPAPIFailureAndInvalidOutputAreErrors(t *testing.T) {
	for _, mode := range []string{"failure", "empty", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			data := []byte{1}
			freed := false
			result, err := cryptData(data, func(in *dataBlob, flags uint32, out *dataBlob) error {
				if mode == "failure" {
					return errInjected
				}
				if mode == "oversize" {
					out.Size = 2 << 20
					out.Data = &data[0]
				}
				return nil
			}, func(*byte) { freed = true })
			if err == nil || result != nil {
				t.Fatal("failed DPAPI reported success")
			}
			if mode == "failure" && !errors.Is(err, errInjected) {
				t.Fatal(err)
			}
			if mode == "oversize" && !freed {
				t.Fatal("allocation not freed")
			}
		})
	}
}

func TestAtomicMoveUsesReplaceAndWriteThrough(t *testing.T) {
	called := false
	err := moveFile(`C:\fake\backup.tmp`, `C:\fake\backup`, func(from, to *uint16, flags uint32) error {
		called = true
		f, _ := readUTF16(from)
		dest, _ := readUTF16(to)
		if flags != 9 || f != `C:\fake\backup.tmp` || dest != `C:\fake\backup` {
			t.Fatal("wrong atomic move arguments")
		}
		return errInjected
	})
	if !called || !errors.Is(err, errInjected) {
		t.Fatalf("called=%v err=%v", called, err)
	}
}
