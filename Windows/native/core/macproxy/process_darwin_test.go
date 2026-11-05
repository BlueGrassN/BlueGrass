//go:build darwin

package macproxy

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"bluegrass/native/core/winproxy"
	"golang.org/x/sys/unix"
)

// Test-only subprocess fixture. It replaces the external OS helper, not the
// transport under test. Reentry does not request authorization or touch prefs.
func init() {
	executable, _ := os.Executable()
	if filepath.Base(executable) != "BlueGrass.ProxySettings" {
		return
	}
	dir := os.Getenv("BLUEGRASS_PROXY_LOCK_DIRECTORY")
	_ = os.WriteFile(filepath.Join(dir, "fixture-pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
	_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
	time.Sleep(30 * time.Second) // Simulated authorization that completes very late.
	_ = os.WriteFile(filepath.Join(dir, "late-apply"), []byte("unsafe"), 0600)
	os.Exit(0)
}

func TestTimeoutKillsAndReapsRealHelperBeforeReturning(t *testing.T) {
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(dir, "BlueGrass.ProxySettings")
	if err = os.WriteFile(helper, binary, 0700); err != nil {
		t.Fatal(err)
	}
	m, closeHelper, err := Open(helper, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHelper()
	var pid int
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		if data, err := os.ReadFile(filepath.Join(dir, "fixture-pid")); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("helper did not start")
	}
	d := m.driver.(*pipeDriver)
	d.timeout = 100 * time.Millisecond
	if _, err := d.Read(); !errors.Is(err, winproxy.ErrRecoveryRequired) {
		t.Fatal("blocked helper did not time out", err)
	}
	if err := unix.Kill(pid, 0); !errors.Is(err, unix.ESRCH) {
		t.Fatal("timeout returned with live or unreaped helper", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "late-apply")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("late operation ran")
	}
}
