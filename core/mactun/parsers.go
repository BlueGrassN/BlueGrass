package mactun

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"
)

func canonicalRules(raw string) string {
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lines = append(lines, strings.Join(strings.Fields(line), " "))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}
func supportedRootRules(raw string) bool {
	return canonicalRules(raw) == "anchor \"com.apple/*\" all\n"
}
func supportedRootNAT(raw string) bool {
	for _, line := range strings.Split(strings.TrimSpace(canonicalRules(raw)), "\n") {
		if line != "" && line != `nat-anchor "com.apple/*" all` && line != `rdr-anchor "com.apple/*" all` {
			return false
		}
	}
	return true
}
func safePFInterfaces(raw string) bool {
	current := ""
	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			current = strings.Fields(line)[0]
		}
		if strings.Contains(strings.ToLower(line), "skip") && current != "lo0" {
			return false
		}
	}
	return current != ""
}

var tokenPattern = regexp.MustCompile(`(?m)^Token\s*:\s*([0-9]{1,20})\s*$`)

func parsePFToken(raw string) (string, error) {
	matches := tokenPattern.FindAllStringSubmatch(raw, -1)
	if len(matches) != 1 {
		return "", ErrRecovery
	}
	return matches[0][1], nil
}
func parseDNSOutput(raw string) ([]netip.Addr, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "There aren't any DNS Servers set on ") && strings.HasSuffix(raw, ".") {
		return nil, nil
	}
	if raw == "" {
		return nil, errors.New("DNS_READ_FAILED")
	}
	var result []netip.Addr
	for _, line := range strings.Split(raw, "\n") {
		ip, err := netip.ParseAddr(strings.TrimSpace(line))
		if err != nil {
			return nil, errors.New("DNS_READ_FAILED")
		}
		result = append(result, ip)
	}
	if !validAddresses(result) {
		return nil, ErrRecovery
	}
	return result, nil
}

var serviceLine = regexp.MustCompile(`^\(([0-9]+|\*)\) (.+)$`)
var deviceLine = regexp.MustCompile(`^\(Hardware Port: .+, Device: (en[0-9]{1,3})\)$`)

func parseServiceOrder(raw string) ([]Resolver, error) {
	var result []Resolver
	service := ""
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if match := serviceLine.FindStringSubmatch(line); match != nil {
			service = ""
			if match[1] != "*" {
				service = match[2]
			}
			continue
		}
		if match := deviceLine.FindStringSubmatch(line); match != nil && service != "" {
			if !validService(service) || seen[service] || len(result) >= 32 {
				return nil, ErrRecovery
			}
			seen[service] = true
			result = append(result, Resolver{Service: service, Interface: match[1]})
			service = ""
		}
	}
	if len(result) == 0 {
		return nil, errors.New("PHYSICAL_SERVICE_UNAVAILABLE")
	}
	return result, nil
}
