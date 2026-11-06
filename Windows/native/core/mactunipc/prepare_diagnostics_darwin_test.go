//go:build darwin

package mactunipc

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Test-only evidence for a failed prepare, never an alternative trust check.
// Inspect nodes/ACLs, not directory contents, configuration or executable bytes.
func smokePreparePathError(operation, path string, cause error) error {
	var report strings.Builder
	chain := []string{}
	for p := path; ; p = filepath.Dir(p) {
		chain = append(chain, p)
		if p == "/" {
			break
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		record := smokePrepareMetadata(chain[i])
		b, _ := json.Marshal(record)
		report.WriteString("ROOT_METADATA ")
		report.Write(b)
		report.WriteByte('\n')
	}
	return fmt.Errorf("prepare operation=%s path=%q: %w\n%s", operation, path, cause, report.String())
}

func smokePrepareMetadata(path string) map[string]any {
	r := map[string]any{"path": path}
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		r["lstatError"] = err.Error()
		return r
	}
	r["uid"], r["gid"], r["mode"], r["type"] = st.Uid, st.Gid, fmt.Sprintf("%#o", st.Mode&07777), fmt.Sprintf("%#o", st.Mode&unix.S_IFMT)
	r["device"], r["inode"], r["links"] = st.Dev, st.Ino, st.Nlink
	r["rootOwner"], r["groupOrOtherWritable"] = st.Uid == 0, st.Mode&0022 != 0
	if st.Mode&unix.S_IFMT == unix.S_IFDIR || st.Mode&unix.S_IFMT == unix.S_IFREG {
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if err != nil {
			r["openError"] = err.Error()
		} else {
			defer unix.Close(fd)
			r["fdNoACL"] = fdNoACL(fd)
			var fs unix.Statfs_t
			if err := unix.Fstatfs(fd, &fs); err != nil {
				r["statfsError"] = err.Error()
			} else {
				r["filesystemFlags"] = fmt.Sprintf("%#x", fs.Flags)
				r["unknownPermissions"] = fs.Flags&unix.MNT_UNKNOWNPERMISSIONS != 0
			}
			attrs := unix.Attrlist{Bitmapcount: 5, Commonattr: unix.ATTR_CMN_EXTENDED_SECURITY}
			var b [4096]byte
			_, _, errno := unix.Syscall6(unix.SYS_FGETATTRLIST, uintptr(fd), uintptr(unsafe.Pointer(&attrs)), uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
			runtime.KeepAlive(&attrs)
			runtime.KeepAlive(&b)
			if errno != 0 {
				r["securityAttributesError"] = errno.Error()
			} else {
				r["securityTotalBytes"], r["securityOffset"], r["securityLength"] = binary.LittleEndian.Uint32(b[:4]), binary.LittleEndian.Uint32(b[4:8]), binary.LittleEndian.Uint32(b[8:12])
				r["securityPrefixHex"] = hex.EncodeToString(b[:56])
			}
		}
	}
	// Independent human-readable ACL evidence. Fixed tool/options, bounded
	// time/output and JSON escaping prevent unsafe or unbounded CI log data.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/ls", "-ldne", path)
	cmd.Env = smokeEnvironment()
	cmd.WaitDelay = 100 * time.Millisecond
	var output smokeDiagnosticOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		r["lsError"] = err.Error()
	}
	r["lsACL"] = output.String()
	return r
}

type smokeDiagnosticOutput struct{ strings.Builder }

func (b *smokeDiagnosticOutput) Write(p []byte) (int, error) {
	const limit = 8192
	if b.Len()+len(p) > limit {
		if left := limit - b.Len(); left > 0 {
			_, _ = b.Builder.Write(p[:left])
		}
		return 0, fmt.Errorf("root metadata output exceeded %d bytes", limit)
	}
	return b.Builder.Write(p)
}

func TestSmokePrepareDiagnosticsDoNotReadOrModifyContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metadata-only")
	const secret = "DO_NOT_LOG_PROFILE_CONTENTS_5d927e"
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	report := smokePreparePathError("rootFile", path, ErrHelperInvalid).Error()
	if !strings.Contains(report, path) || !strings.Contains(report, "ROOT_METADATA") || !strings.Contains(report, "fdNoACL") {
		t.Fatal("missing path/ACL diagnostics")
	}
	if strings.Contains(report, secret) {
		t.Fatal("diagnostics exposed file contents")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != secret {
		t.Fatal("diagnostics modified file")
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("diagnostics modified permissions")
	}
}
