//go:build darwin

package mactunipc

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestKernelBirthIdentityIsStableAndMatchesSocket(t *testing.T) {
	i, err := processIdentity(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	again, err := processIdentity(uint32(os.Getpid()))
	if err != nil || again != i || i.StartSeconds == 0 {
		t.Fatalf("%+v %+v %v", i, again, err)
	}
	path := filepath.Join(t.TempDir(), "p.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	s, err := l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	uid, pid, err := socketPeer(s)
	if err != nil || uid != uint32(os.Getuid()) || pid != uint32(os.Getpid()) {
		t.Fatalf("uid=%d pid=%d err=%v", uid, pid, err)
	}
	if os.Getuid() != 0 {
		if err = authenticateSocket(s, i); err != nil {
			t.Fatal(err)
		}
	}
	i.StartMicroseconds = (i.StartMicroseconds + 1) % 1000000
	if err = authenticateSocket(s, i); err == nil {
		t.Fatal("reused PID identity accepted")
	}
}

func TestProtectedFilesRejectWritableAncestorsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "file")
	if err := os.WriteFile(p, []byte("not a root helper"), 0600); err != nil {
		t.Fatal(err)
	}
	if f, err := rootFile(p, 0); err == nil {
		f.Close()
		t.Fatal("writable ancestor accepted")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("/bin/sh", link); err != nil {
		t.Fatal(err)
	}
	if f, err := rootFile(link, 0); err == nil {
		f.Close()
		t.Fatal("symlink accepted")
	}
	if err := checkRootDirectory(dir); err == nil {
		t.Fatal("unsafe ancestor accepted")
	}
}

func TestShellQuotingExecutesOnlyLiteralArguments(t *testing.T) {
	for _, value := range []string{"/Applications/Blue Grass.app/a", "/tmp/a'$(echo BAD);`echo BAD`\\b", "line\nbreak"} {
		q, err := shellQuote(value)
		if err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command("/bin/sh", "-c", "/usr/bin/printf '%s' "+q).Output()
		if err != nil || string(out) != value {
			t.Fatalf("%q %v", out, err)
		}
	}
}

func TestRootWorkerRefusesDirectBundleExecutionBeforeMutation(t *testing.T) {
	// The test executable is not the content-addressed installed worker.
	if err := validateInstalledWorker(); err == nil {
		t.Fatal("uninstalled executable accepted")
	}
	if err := Serve(Identity{}, func(io.Reader, io.Writer, uint32) error { t.Fatal("unauthorized callback"); return nil }); err == nil {
		t.Fatal("unauthorized Serve accepted")
	}
}

func TestInstallerShellSyntaxAndArguments(t *testing.T) {
	if out, err := exec.Command("/bin/sh", "-n", "-c", installScript).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	command, err := InstallationCommand("/Applications/a' b.app/Contents/MacOS/BlueGrass.Tunnel", strings.Repeat("a", 64), Identity{501, 42, 123, 456})
	if err != nil {
		t.Fatal(err)
	}
	// Replace the shell executor with printf: observe the actual parsed argv,
	// without running installation or changing any system path.
	quotedArgs := strings.TrimPrefix(command, "/bin/sh -c")
	out, err := exec.Command("/bin/sh", "-c", "/usr/bin/printf '%s\\000' "+quotedArgs).Output()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(out), "\x00")
	if len(parts) != 9 || parts[0] != installScript || parts[1] != "bluegrass-install" || parts[2] != "/Applications/a' b.app/Contents/MacOS/BlueGrass.Tunnel" || parts[4] != "501" || parts[7] != "456" {
		t.Fatalf("argument count/values invalid: %d", len(parts))
	}
}

func TestAuthorizationAppleScriptCompilesWithoutExecuting(t *testing.T) {
	out, err := exec.Command("/usr/bin/osacompile", "-o", filepath.Join(t.TempDir(), "authorization.scpt"), "-e", authorizationScript).CombinedOutput()
	if err != nil {
		t.Fatalf("authorization script compilation: %s %v", out, err)
	}
}
