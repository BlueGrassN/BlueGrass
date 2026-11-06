//go:build darwin

package mactunipc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"bluegrass/native/core/control"
	"golang.org/x/net/route"
	"golang.org/x/sys/unix"
)

const smokeGate = "BLUEGRASS_TEST_DARWIN_ROOT_BOOTSTRAP"
const smokeWitnessPath = canonicalRuntime + "/.bootstrap-smoke.json"
const smokeResultPrefix = "BLUEGRASS_BOOTSTRAP_FIXTURE:"

// These flags exist only in this test executable, never BlueGrass.Tunnel or
// BlueGrass.Native. No worker mode or production environment bypass is added.
var smokeFixtureAction = flag.String("bluegrass-smoke-fixture", "", "internal gated root fixture action")
var smokeFixtureOwner = flag.String("bluegrass-smoke-owner", "", "internal gated fixture kernel owner")

type smokeDirectory struct {
	Device int32  `json:"device"`
	Inode  uint64 `json:"inode"`
}
type smokeWitness struct {
	Owner        Identity                  `json:"owner"`
	Hash         string                    `json:"hash"`
	Directories  map[string]smokeDirectory `json:"directories"`
	IntentParent smokeDirectory            `json:"intentParent"`
	Network      map[string]string         `json:"network"`
}

// TestDarwinRootBootstrapSmoke is opt-in, runs as the ordinary runner UID,
// and talks to the actual signed/installed worker. It never sends Connect.
func TestDarwinRootBootstrapSmoke(t *testing.T) {
	if os.Getenv(smokeGate) != "1" {
		t.Skip("requires an explicitly enabled disposable macOS runner")
	}
	if os.Getuid() == 0 || os.Geteuid() != os.Getuid() {
		t.Fatal("run the smoke test as the ordinary runner UID, not sudo")
	}
	source, err := bundledWorker()
	if err != nil {
		t.Fatalf("signed adjacent worker and injected digest required: %v", err)
	}
	if _, err = smokeCommand("/usr/bin/codesign", "--verify", "--strict", source); err != nil {
		t.Fatal("adjacent worker must already have its final signature")
	}
	broker := filepath.Join(filepath.Dir(source), "BlueGrass.Native")
	info, err := smokeCommand(broker, "--tun-worker-info")
	if err != nil || !bytes.Equal(info, WorkerInfo()) {
		t.Fatal("adjacent broker build-info does not match test's embedded worker digest")
	}
	owner, err := processIdentity(uint32(os.Getpid()))
	if err != nil || !owner.valid() {
		t.Fatalf("kernel broker identity: %v", err)
	}
	if err = smokeRootFixture("prepare", owner); err != nil {
		t.Fatal(err)
	}
	var conn net.Conn
	var worker Identity
	defer func() {
		if conn != nil {
			_ = conn.Close()
		}
		if err := smokeWaitWorkerExit(worker); err != nil {
			t.Error(err)
		}
		if err := smokeRootFixture("cleanup", owner); err != nil {
			t.Errorf("owned fixture cleanup refused (retained for inspection): %v", err)
		}
	}()
	for _, fixture := range []struct{ name, action string }{{"RootOwnedWritableACL", "acl"}, {"DurableIntentWithoutRuntime", "durable"}} {
		if !t.Run(fixture.name, func(t *testing.T) {
			if err := smokeRootFixture(fixture.action, owner); err != nil {
				t.Fatal(err)
			}
		}) {
			t.Fatal("root security fixture failed before installation")
		}
	}
	if !t.Run("StaleRuntimeRecovery", func(t *testing.T) {
		if err = smokeRootFixture("stale", owner); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := authorizationContext(context.Background())
		defer cancel()
		command, err := InstallationCommand(source, ExpectedWorkerSHA256, owner)
		if err != nil {
			t.Fatal(err)
		}
		// Only the authorizer changes. The shell command, pre/post-copy checks,
		// installed signed worker, numeric identity and socket protocol are real.
		conn, err = authorizeAndDial(ctx, func(ctx context.Context) error {
			cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "--", "/bin/sh", "-c", command)
			cmd.Env = smokeEnvironment()
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			cmd.WaitDelay = 100 * time.Millisecond
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("fixed sudo installer failed: %w", err)
			}
			return nil
		}, dialWorker)
		if err != nil {
			t.Fatal(err)
		}
		u, pid, err := socketPeer(conn.(*net.UnixConn))
		if err != nil || u != 0 {
			t.Fatal("worker peer is not kernel-authenticated root")
		}
		worker, err = processIdentity(pid)
		if err != nil || worker.UID != 0 || worker.StartSeconds == 0 {
			t.Fatal("root worker kernel birth identity unavailable")
		}
		client := &Client{conn: conn, timeout: FrameTimeout}
		// A ready frame + successful Hello proves the root server accepted this
		// ordinary process's actual UID/PID/birth identity, not asserted JSON fields.
		for _, command := range []string{"Hello", "Disconnect"} {
			request, _ := json.Marshal(map[string]string{"id": "bootstrap-" + command, "command": command})
			reply, err := client.Exchange(request)
			if err != nil {
				t.Fatalf("%s: %v", command, err)
			}
			if err = smokeDisconnectedReply(reply, "bootstrap-"+command); err != nil {
				t.Fatalf("%s: %v", command, err)
			}
		}
		if err = client.Close(); err != nil {
			t.Fatal(err)
		}
		if err = smokeWaitWorkerExit(worker); err != nil {
			t.Fatal(err)
		}
		t.Log("signed worker recovered stale socket/lock, authenticated UID/PID/kernel birth; Hello and explicit Disconnect stayed inactive; worker/socket exited")
	}) {
		t.Fatal("signed worker bootstrap with durable stale runtime failed")
	}
}

func smokeDisconnectedReply(data []byte, id string) error {
	var r struct {
		ID       string         `json:"id"`
		OK       bool           `json:"ok"`
		Error    string         `json:"error"`
		Snapshot map[string]any `json:"snapshot"`
	}
	if control.ValidateJSON(data) != nil || json.Unmarshal(data, &r) != nil || !r.OK || r.ID != id || r.Error != "" ||
		r.Snapshot["protectionIntent"] != false || r.Snapshot["businessGateOpen"] != false || r.Snapshot["gateKnown"] != true ||
		r.Snapshot["connection"] != "disconnected" || r.Snapshot["actualMode"] != "none" {
		return errors.New("fresh worker did not report verified inactive state")
	}
	return nil
}

func smokeWaitWorkerExit(worker Identity) error {
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		_, socketErr := os.Lstat(canonicalRuntime + "/worker.sock")
		live, err := processIdentity(worker.PID)
		if errors.Is(socketErr, os.ErrNotExist) && (worker.PID == 0 || err != nil || live != worker) {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.New("root worker/socket did not exit within six seconds")
}

func smokeEnvironment() []string {
	return []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C", "LANG=C"}
}

type smokeOutput struct{ bytes.Buffer }

func (b *smokeOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1024*1024 {
		return 0, io.ErrShortBuffer
	}
	return b.Buffer.Write(p)
}

func smokeCommand(path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = smokeEnvironment()
	cmd.WaitDelay = 100 * time.Millisecond
	var out smokeOutput
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("read-only fixture command %s failed: %w", path, err)
	}
	return out.Bytes(), nil
}

func smokeRootFixture(action string, owner Identity) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	identity := fmt.Sprintf("%d,%d,%d,%d", owner.UID, owner.PID, owner.StartSeconds, owner.StartMicroseconds)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sudo", "-n", "--", "/usr/bin/env", smokeGate+"=1", exe,
		"-test.run=^TestDarwinRootBootstrapFixture$", "-test.timeout=30s", "-bluegrass-smoke-fixture="+action, "-bluegrass-smoke-owner="+identity)
	cmd.Env = smokeEnvironment()
	cmd.WaitDelay = 100 * time.Millisecond
	var out smokeOutput
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("root fixture %s: %w\n%s", action, err, out.String())
	}
	if !strings.Contains(out.String(), smokeResultPrefix+action+":OK\n") {
		return fmt.Errorf("root fixture %s did not acknowledge success", action)
	}
	return nil
}

// This separate test-only process performs root fixture ownership checks and
// read-only network snapshots. Cleanup has no recursive remove and cannot
// select a path from environment, flags or arbitrary request data.
func TestDarwinRootBootstrapFixture(t *testing.T) {
	if os.Getenv(smokeGate) != "1" || *smokeFixtureAction == "" {
		t.Skip("internal opt-in fixture subprocess")
	}
	if os.Getuid() != 0 || os.Geteuid() != 0 || !validHash(ExpectedWorkerSHA256) {
		t.Fatal("root fixture requires sudo and an injected signed-worker digest")
	}
	parts := strings.Split(*smokeFixtureOwner, ",")
	if len(parts) != 4 {
		t.Fatal("invalid owner")
	}
	owner, err := ParseOwner([]string{"--owner-uid", parts[0], "--owner-pid", parts[1], "--owner-start-sec", parts[2], "--owner-start-usec", parts[3]})
	sudoUID, uidErr := strconv.ParseUint(os.Getenv("SUDO_UID"), 10, 32)
	if err != nil || uidErr != nil || uint32(sudoUID) != owner.UID || !ownerAlive(owner) {
		t.Fatal("fixture is not owned by the live sudo caller")
	}
	switch *smokeFixtureAction {
	case "prepare":
		err = smokePrepare(owner)
	case "cleanup":
		err = smokeCleanup(owner)
	case "acl":
		err = smokeACLFixture(owner)
	case "durable":
		err = smokeDurableFixture(owner)
	case "stale":
		err = smokeStaleRuntimeFixture(owner)
	default:
		t.Fatal("unknown fixed fixture action")
	}
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("%s%s:OK\n", smokeResultPrefix, *smokeFixtureAction)
}

func smokePrepare(owner Identity) (retErr error) {
	// Refuse even inactive/stale directories: never adopt somebody else's
	// journal, intent, lock, helper or root-owned fixture.
	for _, p := range []string{canonicalRuntime, StateDirectory, IntentPath, helperPath(ExpectedWorkerSHA256)} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("fresh-runner prerequisite failed at %s", p)
		}
	}
	network, err := smokeNetworkSnapshot()
	if err != nil {
		return err
	}
	w := smokeWitness{Owner: owner, Hash: ExpectedWorkerSHA256, Directories: map[string]smokeDirectory{}, Network: network}
	created := []string{}
	defer func() {
		if retErr != nil {
			for i := len(created) - 1; i >= 0; i-- {
				if id, err := smokeDirectoryIdentity(created[i]); err == nil && id == w.Directories[created[i]] {
					_ = os.Remove(created[i]) // only the same, still-empty directory
				}
			}
		}
	}()
	for _, p := range []string{helperDirectory, filepath.Dir(StateDirectory), canonicalRuntime, StateDirectory} {
		mode := os.FileMode(0755)
		if p == StateDirectory {
			mode = 0700
		}
		if err = checkRootDirectory(filepath.Dir(p)); err != nil {
			return smokePreparePathError("check parent directory", filepath.Dir(p), err)
		}
		if _, err = os.Lstat(p); errors.Is(err, os.ErrNotExist) {
			if err = os.Mkdir(p, mode); err != nil {
				return smokePreparePathError("create reserved directory", p, err)
			}
			created = append(created, p)
			id, err := smokeDirectoryIdentity(p)
			if err != nil {
				return smokePreparePathError("record created directory identity", p, err)
			}
			w.Directories[p] = id
		} else if err != nil {
			return smokePreparePathError("lstat reserved directory", p, err)
		} else if p == canonicalRuntime || p == StateDirectory {
			return errors.New("runtime/state appeared during fixture reservation")
		}
		if err = checkRootDirectory(p); err != nil {
			return smokePreparePathError("check reserved directory", p, err)
		}
	}
	w.IntentParent, err = smokeDirectoryIdentity(filepath.Dir(IntentPath))
	if err != nil {
		return smokePreparePathError("record durable intent parent identity", filepath.Dir(IntentPath), err)
	}
	raw, err := json.Marshal(w)
	if err != nil {
		return err
	}
	f, err := rootFile(smokeWitnessPath, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY)
	if err != nil {
		return smokePreparePathError("open root-owned witness", smokeWitnessPath, err)
	}
	_, writeErr := f.Write(raw)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(smokeWitnessPath)
		return errors.Join(writeErr, syncErr, closeErr)
	}
	return nil
}

func smokeDirectoryIdentity(p string) (smokeDirectory, error) {
	var s unix.Stat_t
	if err := checkRootDirectory(p); err != nil {
		return smokeDirectory{}, err
	}
	if err := unix.Lstat(p, &s); err != nil {
		return smokeDirectory{}, err
	}
	return smokeDirectory{Device: s.Dev, Inode: s.Ino}, nil
}

func smokeCleanup(owner Identity) error {
	f, err := rootFile(smokeWitnessPath, unix.O_RDONLY)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16385))
	_ = f.Close()
	if err != nil || len(raw) > 16384 {
		return errors.New("invalid owned fixture witness")
	}
	var w smokeWitness
	if json.Unmarshal(raw, &w) != nil || w.Owner != owner || w.Hash != ExpectedWorkerSHA256 {
		return errors.New("fixture owner/hash changed; refusing cleanup")
	}
	parent, err := smokeDirectoryIdentity(filepath.Dir(IntentPath))
	if err != nil || parent != w.IntentParent {
		return errors.New("durable intent parent replaced; refusing cleanup")
	}
	allowedDirectories := map[string]bool{canonicalRuntime: true, StateDirectory: true, helperDirectory: true, filepath.Dir(StateDirectory): true}
	if len(w.Directories) < 2 || len(w.Directories) > 4 {
		return errors.New("invalid created-directory witness")
	}
	for p, id := range w.Directories {
		if !allowedDirectories[p] {
			return errors.New("unexpected fixture directory")
		}
		actual, err := smokeDirectoryIdentity(p)
		if err != nil || actual != id {
			return errors.New("owned fixture directory was replaced")
		}
	}
	if _, ok := w.Directories[canonicalRuntime]; !ok {
		return errors.New("runtime directory was not created by fixture")
	}
	if _, ok := w.Directories[StateDirectory]; !ok {
		return errors.New("state directory was not created by fixture")
	}
	// Take both real leases before checking or removing any owned state. An
	// active worker/manager, another user, marker or journal always wins.
	for _, p := range []string{canonicalRuntime + "/worker.lock", StateDirectory + "/network.lock"} {
		lock, err := rootFile(p, unix.O_RDWR)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		defer lock.Close()
		if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
			return errors.New("live worker/manager lease; refusing cleanup")
		}
	}
	if _, err := os.Lstat(IntentPath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("durable intent remains; refusing cleanup")
	}
	for p, allowed := range map[string]map[string]bool{
		canonicalRuntime: {".bootstrap-smoke.json": true, "worker.lock": true},
		StateDirectory:   {"network.lock": true, "fakeip.db": true},
	} {
		entries, err := os.ReadDir(p)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !allowed[entry.Name()] {
				return fmt.Errorf("unexpected state %s; refusing cleanup", entry.Name())
			}
			f, err := rootFile(filepath.Join(p, entry.Name()), unix.O_RDONLY)
			if err != nil {
				return err
			}
			_ = f.Close()
			if p == StateDirectory && entry.Name() == "fakeip.db" {
				if err := smokeProtectedReleasedCache(); err != nil {
					return err
				}
			}
		}
	}
	after, err := smokeNetworkSnapshot()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(w.Network, after) {
		return errors.New("routes/DNS/interfaces/PF changed; retaining fixture for inspection")
	}
	helper := helperPath(ExpectedWorkerSHA256)
	if f, err := rootFile(helper, unix.O_RDONLY); err == nil {
		hash, hashErr := hashFile(f)
		_ = f.Close()
		if hashErr != nil || hash != w.Hash {
			return errors.New("owned helper changed; refusing deletion")
		}
		if err = os.Remove(helper); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, p := range []string{StateDirectory + "/fakeip.db", StateDirectory + "/network.lock", canonicalRuntime + "/worker.lock", smokeWitnessPath} {
		if err = os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, p := range []string{StateDirectory, canonicalRuntime, filepath.Dir(StateDirectory), helperDirectory} {
		if _, created := w.Directories[p]; created {
			if err = os.Remove(p); err != nil {
				return err
			} // empty directories only
		}
	}
	return nil
}

// Explicit Disconnect clears even an inactive persistent cache by leaving a
// valid, empty DB. It is an owned fixture file, not a recovery journal. Never
// clear/repair its contents here: accept only the already-cleared database.
func smokeProtectedReleasedCache() error {
	path := StateDirectory + "/fakeip.db"
	return smokeEmptyReleasedCache(path, func(name string, _ int, _ os.FileMode) (*os.File, error) {
		if name != path {
			return nil, errors.New("unexpected released cache path")
		}
		f, err := rootFile(path, unix.O_RDONLY)
		if err != nil {
			return nil, err
		}
		info, err := f.Stat()
		if err != nil || info.Mode().Perm() != 0600 || info.Size() < 0 || info.Size() > 32<<20 {
			_ = f.Close()
			return nil, errors.New("unsafe released cache metadata")
		}
		return f, nil
	})
}

func smokeNetworkSnapshot() (map[string]string, error) {
	snapshot := map[string]string{}
	digest := func(key string, data []byte) { h := sha256.Sum256(data); snapshot[key] = hex.EncodeToString(h[:]) }
	for _, family := range []int{unix.AF_INET, unix.AF_INET6} {
		rib, err := route.FetchRIB(family, route.RIBTypeRoute, 0)
		if err != nil {
			return nil, err
		}
		messages, err := route.ParseRIB(route.RIBTypeRoute, rib)
		if err != nil {
			return nil, err
		}
		rows := []string{}
		for _, message := range messages {
			if r, ok := message.(*route.RouteMessage); ok {
				addresses, err := json.Marshal(r.Addrs)
				if err != nil {
					return nil, err
				}
				// Exclude packet/use/expiry metrics, retaining routes, masks,
				// gateways, interface indices and flags for both IP families.
				rows = append(rows, fmt.Sprintf("%d %d %s", r.Index, r.Flags, addresses))
			}
		}
		sort.Strings(rows)
		digest(fmt.Sprintf("routes-%d", family), []byte(strings.Join(rows, "\n")))
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	interfaceRows := []string{}
	for _, i := range interfaces {
		interfaceRows = append(interfaceRows, fmt.Sprintf("%d %s %d %d", i.Index, i.Name, i.MTU, i.Flags))
	}
	sort.Strings(interfaceRows)
	digest("interfaces", []byte(strings.Join(interfaceRows, "\n")))
	dns, err := smokeCommand("/usr/sbin/scutil", "--dns")
	if err != nil {
		return nil, err
	}
	digest("dns", dns)
	for _, query := range []struct {
		key  string
		args []string
	}{
		{"pf-anchor", []string{"-a", "com.apple/zz.bluegrass", "-sr"}},
		{"pf-rules", []string{"-sr"}},
		{"pf-nat", []string{"-sn"}},
		{"pf-references", []string{"-s", "References"}},
		{"pf-status", []string{"-s", "info"}},
	} {
		out, err := smokeCommand("/sbin/pfctl", query.args...)
		if err != nil {
			return nil, err
		}
		if query.key == "pf-anchor" && strings.TrimSpace(string(out)) != "" {
			return nil, errors.New("existing product PF anchor; refusing fresh bootstrap")
		}
		if query.key == "pf-status" {
			status := ""
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "Status:") {
					fields := strings.Fields(line)
					if len(fields) >= 2 {
						status = fields[1]
					}
				}
			}
			if status != "Enabled" && status != "Disabled" {
				return nil, errors.New("unknown PF status")
			}
			out = []byte(status) // uptime and traffic counters naturally change
		}
		digest(query.key, out)
	}
	return snapshot, nil
}
