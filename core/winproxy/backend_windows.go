//go:build windows

package winproxy

import (
	"errors"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

// uint64 gives the union its native size/alignment: 8/8 on 64-bit and 8/4
// on 386, matching DWORD, LPWSTR and FILETIME in INTERNET_PER_CONN_OPTIONW.
type connectionOption struct {
	Option uint32
	Value  uint64
}
type connectionOptions struct {
	Size        uint32
	Connection  *uint16
	Count       uint32
	OptionError uint32
	Options     *connectionOption
}
type winInetBackend struct {
	query  func(*connectionOptions) error
	set    func(*connectionOptions) error
	notify func(uint32) error
	free   func(*uint16)
}

func (b *winInetBackend) Query() (Settings, error) {
	s, err := b.queryFlags(10) // FLAGS_UI preserves the user's auto-detect choice.
	if errors.Is(err, syscall.Errno(87)) || errors.Is(err, syscall.Errno(12009)) {
		return b.queryFlags(1) // Legacy INVALID_PARAMETER / INVALID_OPTION only.
	}
	return s, err
}

func optionList(options *[4]connectionOption) connectionOptions {
	return connectionOptions{Size: uint32(unsafe.Sizeof(connectionOptions{})), Count: 4, Options: &options[0]}
}

func (b *winInetBackend) queryFlags(flagOption uint32) (Settings, error) {
	options := [4]connectionOption{{Option: flagOption}, {Option: 2}, {Option: 3}, {Option: 4}}
	list := optionList(&options)
	defer func() {
		for i := 1; i < len(options); i++ {
			if p := *(**uint16)(unsafe.Pointer(&options[i].Value)); p != nil {
				b.free(p)
			}
		}
	}()
	if err := b.query(&list); err != nil {
		return Settings{}, err
	}
	var values [3]string
	for i := range values {
		p := *(**uint16)(unsafe.Pointer(&options[i+1].Value))
		v, err := readUTF16(p)
		if err != nil {
			return Settings{}, err
		}
		values[i] = v
	}
	return Settings{Flags: uint32(options[0].Value), Server: values[0], Bypass: values[1], AutoConfigURL: values[2]}, nil
}

func readUTF16(p *uint16) (string, error) {
	if p == nil {
		return "", nil
	}
	var units []uint16
	for i := 0; i < maxRecordBytes/2; i++ {
		v := *(*uint16)(unsafe.Add(unsafe.Pointer(p), i*2))
		if v == 0 {
			// Reject malformed UTF-16; replacement characters would lose the original.
			for j := 0; j < len(units); j++ {
				if units[j] >= 0xd800 && units[j] <= 0xdbff {
					if j+1 == len(units) || units[j+1] < 0xdc00 || units[j+1] > 0xdfff {
						return "", ErrInvalidSettings
					}
					j++
				} else if units[j] >= 0xdc00 && units[j] <= 0xdfff {
					return "", ErrInvalidSettings
				}
			}
			return string(utf16.Decode(units)), nil
		}
		units = append(units, v)
	}
	return "", ErrInvalidSettings
}

func (b *winInetBackend) Set(s Settings) error {
	if !validSettings(s) {
		return ErrInvalidSettings
	}
	options := [4]connectionOption{{Option: 1, Value: uint64(s.Flags)}, {Option: 2}, {Option: 3}, {Option: 4}}
	var buffers [3][]uint16
	for i, value := range []string{s.Server, s.Bypass, s.AutoConfigURL} {
		buf, err := syscall.UTF16FromString(value)
		if err != nil {
			return ErrInvalidSettings
		}
		buffers[i] = buf
		*(**uint16)(unsafe.Pointer(&options[i+1].Value)) = &buf[0]
	}
	list := optionList(&options)
	err := b.set(&list)
	// The native union stores pointer bits, which Go's GC cannot see.
	runtime.KeepAlive(buffers)
	return err
}

func (b *winInetBackend) Notify() error {
	changed := b.notify(39) // INTERNET_OPTION_SETTINGS_CHANGED
	refresh := b.notify(37) // INTERNET_OPTION_REFRESH, even if the first call fails
	return errors.Join(changed, refresh)
}
