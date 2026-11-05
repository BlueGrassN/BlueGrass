package update

import (
	"errors"
	"strconv"
	"strings"
)

type version struct {
	numbers [3]uint64
	pre     []string
}

func parseVersion(s string, osVersion bool) (version, error) {
	var v version
	if len(s) == 0 || len(s) > 80 {
		return v, errors.New("invalid version")
	}
	base, pre, hasPre := strings.Cut(s, "-")
	if osVersion && hasPre {
		return v, errors.New("invalid OS version")
	}
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return v, errors.New("version needs three components")
	}
	for i, p := range parts {
		n, err := decimal(p)
		if err != nil {
			return v, err
		}
		v.numbers[i] = n
	}
	if hasPre {
		v.pre = strings.Split(pre, ".")
		for _, p := range v.pre {
			if p == "" {
				return v, errors.New("empty prerelease component")
			}
			numeric := true
			for _, c := range p {
				if c < '0' || c > '9' {
					numeric = false
				}
				if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '-') {
					return v, errors.New("invalid prerelease")
				}
			}
			if numeric && len(p) > 1 && p[0] == '0' {
				return v, errors.New("leading zero in prerelease")
			}
		}
	}
	return v, nil
}

func decimal(s string) (uint64, error) {
	if s == "" || len(s) > 1 && s[0] == '0' {
		return 0, errors.New("invalid version component")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid version component")
		}
	}
	return strconv.ParseUint(s, 10, 32)
}

func compareVersion(a, b version) int {
	for i, n := range a.numbers {
		if n < b.numbers[i] {
			return -1
		}
		if n > b.numbers[i] {
			return 1
		}
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	for i, p := range a.pre {
		if i >= len(b.pre) {
			return 1
		}
		q := b.pre[i]
		if p == q {
			continue
		}
		pn, qn := isDigits(p), isDigits(q)
		if pn && qn && len(p) != len(q) {
			if len(p) < len(q) {
				return -1
			}
			return 1
		}
		if pn != qn {
			if pn {
				return -1
			}
			return 1
		}
		if p < q {
			return -1
		}
		return 1
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	return 0
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
