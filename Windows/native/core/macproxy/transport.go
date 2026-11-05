package macproxy

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"bluegrass/native/core/winproxy"
)

const maxMessage = 1024 * 1024

type helperRequest struct {
	Operation     string    `json:"operation"`
	TimeoutMillis int64     `json:"timeoutMillis"`
	Record        *Record   `json:"record,omitempty"`
	Expected      *Snapshot `json:"expected,omitempty"`
	Desired       *Snapshot `json:"desired,omitempty"`
}
type helperResponse struct {
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Snapshot *Snapshot `json:"snapshot,omitempty"`
	Record   *Record   `json:"record,omitempty"`
}
type pipeDriver struct {
	mu       sync.Mutex
	in       io.WriteCloser
	reader   *bufio.Scanner
	stop     func()
	once     sync.Once
	closed   atomic.Bool
	timeout  time.Duration
	deadline time.Time
}

func (d *pipeDriver) beginOperation(deadline time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.deadline = deadline
}
func (d *pipeDriver) endOperation() { d.mu.Lock(); defer d.mu.Unlock(); d.deadline = time.Time{} }

func newPipeDriver(in io.WriteCloser, out io.Reader, stop func(), timeout time.Duration) *pipeDriver {
	s := bufio.NewScanner(out)
	s.Buffer(make([]byte, 4096), maxMessage)
	return &pipeDriver{in: in, reader: s, stop: stop, timeout: timeout}
}
func (d *pipeDriver) close() { d.closed.Store(true); d.once.Do(d.stop) }
func (d *pipeDriver) call(q helperRequest) (helperResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed.Load() {
		return helperResponse{}, winproxy.ErrRecoveryRequired
	}
	remaining := d.timeout
	if !d.deadline.IsZero() {
		if until := time.Until(d.deadline); until < remaining {
			remaining = until
		}
	}
	if remaining < time.Millisecond {
		d.close()
		return helperResponse{}, winproxy.ErrRecoveryRequired
	}
	callDeadline := time.Now().Add(remaining)
	q.TimeoutMillis = remaining.Milliseconds()
	b, err := encode(q)
	if err != nil || len(b) >= maxMessage {
		return helperResponse{}, winproxy.ErrInvalidSettings
	}
	type result struct {
		r   helperResponse
		err error
	}
	done := make(chan result, 1)
	go func() {
		if _, err := d.in.Write(append(b, '\n')); err != nil {
			done <- result{err: winproxy.ErrRecoveryRequired}
			return
		}
		var r helperResponse
		if !d.reader.Scan() || decode(d.reader.Bytes(), &r) != nil {
			done <- result{err: winproxy.ErrRecoveryRequired}
			return
		}
		done <- result{r: r}
	}()
	timer := time.NewTimer(time.Until(callDeadline))
	defer timer.Stop()
	select {
	case v := <-done:
		if !time.Now().Before(callDeadline) {
			d.close()
			return helperResponse{}, winproxy.ErrRecoveryRequired
		}
		if v.err != nil {
			d.close()
			return helperResponse{}, v.err
		}
		if !v.r.OK {
			switch v.r.Error {
			case "OWNERSHIP_LOST":
				return v.r, winproxy.ErrOwnershipLost
			case "INVALID_SETTINGS":
				return v.r, winproxy.ErrInvalidSettings
			case "INVALID_RECORD":
				return v.r, winproxy.ErrInvalidRecord
			case "SETTINGS_VERIFICATION_FAILED":
				return v.r, winproxy.ErrVerificationFailed
			default:
				return v.r, winproxy.ErrRecoveryRequired
			}
		}
		return v.r, nil
	case <-timer.C:
		d.close()
		return helperResponse{}, winproxy.ErrRecoveryRequired
	}
}
func (d *pipeDriver) Read() (Snapshot, error) {
	r, e := d.call(helperRequest{Operation: "read"})
	if e != nil {
		return Snapshot{}, e
	}
	if r.Snapshot == nil {
		return Snapshot{}, winproxy.ErrRecoveryRequired
	}
	return *r.Snapshot, nil
}
func (d *pipeDriver) Load() (*Record, error) {
	r, e := d.call(helperRequest{Operation: "load"})
	return r.Record, e
}
func (d *pipeDriver) Save(r Record) error {
	_, e := d.call(helperRequest{Operation: "save", Record: &r})
	return e
}
func (d *pipeDriver) Delete() error { _, e := d.call(helperRequest{Operation: "delete"}); return e }
func (d *pipeDriver) CAS(a, b Snapshot) error {
	_, e := d.call(helperRequest{Operation: "cas", Expected: &a, Desired: &b})
	return e
}

// Open starts only the sibling helper, without arguments, inherited environment
// secrets, or diagnostic forwarding. Settings travel exclusively over pipes.
func Open(helper, lockDirectory string) (*Manager, func(), error) {
	if !filepath.IsAbs(helper) || filepath.Base(helper) != "BlueGrass.ProxySettings" {
		return nil, nil, winproxy.ErrInvalidSettings
	}
	cmd := exec.Command(helper)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "BLUEGRASS_PROXY_LOCK_DIRECTORY=" + lockDirectory,
		"BLUEGRASS_PROXY_PARENT_PID=" + strconv.Itoa(os.Getpid())}
	cmd.Stderr = io.Discard
	// Only this process holds the writer. The kernel closes it on abrupt host
	// death, independently of deferred cleanup or the Swift protocol thread.
	cancelRead, cancelWrite, err := os.Pipe()
	if err != nil {
		return nil, nil, winproxy.ErrRecoveryRequired
	}
	cmd.ExtraFiles = []*os.File{cancelRead}
	in, err := cmd.StdinPipe()
	if err != nil {
		_ = cancelRead.Close()
		_ = cancelWrite.Close()
		return nil, nil, winproxy.ErrRecoveryRequired
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = in.Close()
		_ = cancelRead.Close()
		_ = cancelWrite.Close()
		return nil, nil, winproxy.ErrRecoveryRequired
	}
	if cmd.Start() != nil {
		_ = cancelRead.Close()
		_ = cancelWrite.Close()
		_ = in.Close()
		_ = out.Close()
		return nil, nil, winproxy.ErrRecoveryRequired
	}
	_ = cancelRead.Close()
	wait := make(chan struct{})
	go func() { _ = cmd.Wait(); close(wait) }()
	d := newPipeDriver(in, out, func() {
		_ = cancelWrite.Close()
		_ = cmd.Process.Kill()
		_ = in.Close()
		_ = out.Close()
		// Wait is started once above. Kill/reap is part of the manager budget,
		// and completes before a timed out RPC returns or ownership is released.
		select {
		case <-wait:
		case <-time.After(helperReapBudget):
		}
	}, ApplyBudget-helperReapBudget)
	closeHelper := func() {
		d.close()
	}
	return NewWithDriver(d), closeHelper, nil
}
