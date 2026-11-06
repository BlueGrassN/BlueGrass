//go:build darwin

package mactun

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var anchorComponent = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,128}$`)

func (b *darwinBackend) CheckPF(ctx context.Context, _ bool) error {
	root, _, err := systemCommand(ctx, "", "/sbin/pfctl", "-sr")
	if err != nil {
		return err
	}
	if !supportedRootRules(root) {
		return ErrUnsupportedPF
	}
	nat, _, err := systemCommand(ctx, "", "/sbin/pfctl", "-sn")
	if err != nil {
		return err
	}
	if !supportedRootNAT(nat) {
		return ErrUnsupportedPF
	}
	for _, anchor := range []string{"com.apple", anchorName} {
		nat, _, err = systemCommand(ctx, "", "/sbin/pfctl", "-a", anchor, "-sn")
		if err != nil {
			return err
		}
		if strings.TrimSpace(nat) != "" {
			return ErrUnsupportedPF
		}
	}
	parent, _, err := systemCommand(ctx, "", "/sbin/pfctl", "-a", "com.apple", "-sr")
	if err != nil {
		return err
	}
	if strings.TrimSpace(parent) != "" {
		return ErrUnsupportedPF
	}
	children, _, err := systemCommand(ctx, "", "/sbin/pfctl", "-a", "com.apple", "-s", "Anchors")
	if err != nil {
		return err
	}
	for _, name := range strings.Fields(children) {
		name = strings.TrimPrefix(name, "com.apple/")
		if name == "zz.bluegrass" {
			continue
		}
		if !anchorComponent.MatchString(name) {
			return ErrUnsupportedPF
		}
		rules, _, e := systemCommand(ctx, "", "/sbin/pfctl", "-a", "com.apple/"+name, "-sr")
		if e != nil {
			return e
		}
		if strings.TrimSpace(rules) != "" {
			return ErrUnsupportedPF
		}
		nat, _, e := systemCommand(ctx, "", "/sbin/pfctl", "-a", "com.apple/"+name, "-sn")
		if e != nil {
			return e
		}
		if strings.TrimSpace(nat) != "" {
			return ErrUnsupportedPF
		}
		nested, _, e := systemCommand(ctx, "", "/sbin/pfctl", "-a", "com.apple/"+name, "-s", "Anchors")
		if e != nil {
			return e
		}
		if strings.TrimSpace(nested) != "" {
			return ErrUnsupportedPF
		}
	}
	interfaces, _, err := systemCommand(ctx, "", "/sbin/pfctl", "-s", "Interfaces", "-v")
	if err != nil {
		return err
	}
	if !safePFInterfaces(interfaces) {
		return ErrUnsupportedPF
	}
	states, _, err := systemCommand(ctx, "", "/sbin/pfctl", "-ss")
	if err != nil {
		return err
	}
	if strings.TrimSpace(states) != "" {
		return ErrUnsupportedPF
	}
	return nil
}
func (b *darwinBackend) ReadPF(ctx context.Context) (string, error) {
	out, _, err := systemCommand(ctx, "", "/sbin/pfctl", "-a", anchorName, "-sr")
	if err != nil {
		return "", err
	}
	return canonicalRules(out), nil
}
func (b *darwinBackend) CompilePF(ctx context.Context, rules string) (string, error) {
	out, _, err := systemCommand(ctx, rules, "/sbin/pfctl", "-a", anchorName, "-o", "none", "-n", "-v", "-f", "-")
	if err != nil {
		return "", err
	}
	return canonicalRules(out), nil
}
func (b *darwinBackend) WritePF(ctx context.Context, rules string) error {
	if rules != "" {
		if err := validateGuardRules(rules); err != nil {
			return err
		}
	}
	_, _, err := systemCommand(ctx, rules, "/sbin/pfctl", "-a", anchorName, "-o", "none", "-f", "-")
	return err
}
func (b *darwinBackend) EnablePF(ctx context.Context) (string, error) {
	out, stderr, err := systemCommand(ctx, "", "/sbin/pfctl", "-E")
	if err != nil {
		return "", err
	}
	return parsePFToken(out + "\n" + stderr)
}
func (b *darwinBackend) PFEnabled(ctx context.Context) error {
	out, _, err := systemCommand(ctx, "", "/sbin/pfctl", "-s", "info")
	if err != nil {
		return err
	}
	if !strings.Contains(out, "Status: Enabled") {
		return errors.New("PF_NOT_ENABLED")
	}
	return nil
}
func (b *darwinBackend) ReleasePF(ctx context.Context, token string) error {
	if token == "" || len(token) > 20 {
		return ErrRecovery
	}
	for _, c := range token {
		if c < '0' || c > '9' {
			return ErrRecovery
		}
	}
	exists, err := b.TokenExists(ctx, token)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, _, commandErr := systemCommand(ctx, "", "/sbin/pfctl", "-X", token)
	exists, err = b.TokenExists(ctx, token)
	if err != nil {
		return errors.Join(commandErr, err)
	}
	if !exists {
		return nil
	}
	return errors.Join(commandErr, ErrRecovery)
}
