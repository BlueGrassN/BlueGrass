package mactunipc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"time"

	"bluegrass/native/core/control"
)

const (
	// FrameTimeout is one complete request/reply operation, not a new budget
	// for each read or write. It covers Prepare, gate setup and their cleanup.
	FrameTimeout = 100 * time.Second
	IdleTimeout  = 30 * time.Second
)

type streamTimeouts struct{ idle, operation, cleanup time.Duration }

// ServeStream is called only after kernel peer authentication. EOF, malformed
// frames, timeouts, cancellation and output failure all close the host reader.
// The host's Close must close forwarding while preserving the manager journal.
func ServeStream(ctx context.Context, conn net.Conn, ownerUID uint32, run func(io.Reader, io.Writer, uint32) error) error {
	return serveStream(ctx, conn, ownerUID, run, streamTimeouts{IdleTimeout, FrameTimeout, time.Second})
}

func serveStream(parent context.Context, conn net.Conn, ownerUID uint32, run func(io.Reader, io.Writer, uint32) error, limits streamTimeouts) error {
	defer conn.Close()
	r, w := io.Pipe()
	defer r.Close()
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	shutdown := func(err error) { cancel(err) }
	closeInput := func() { _ = conn.Close(); _ = w.CloseWithError(context.Cause(ctx)) }
	stop := context.AfterFunc(ctx, closeInput)
	defer stop()
	if err := conn.SetReadDeadline(time.Now().Add(limits.idle)); err != nil {
		return err
	}
	requests := make(chan []byte, 1)
	// Keep reading even during a long operation, so EOF revokes forwarding
	// promptly. Only one bounded request may wait behind the current request.
	go func() {
		for {
			data, err := ReadFrame(conn)
			if err != nil {
				shutdown(err)
				return
			}
			data, err = compactObject(data)
			if err != nil {
				shutdown(err)
				return
			}
			select {
			case requests <- data:
			case <-ctx.Done():
				return
			default:
				shutdown(ErrFrame)
				return
			}
		}
	}()
	responses := &responseWriter{conn: conn, fail: shutdown}
	go func() {
		for {
			var data []byte
			select {
			case data = <-requests:
			case <-ctx.Done():
				return
			}
			if ctx.Err() != nil {
				return
			}
			deadline := time.Now().Add(limits.operation)
			completed := make(chan struct{}, 1)
			if err := responses.begin(deadline, completed); err != nil {
				shutdown(err)
				return
			}
			timer := time.AfterFunc(time.Until(deadline), func() { shutdown(context.DeadlineExceeded) })
			if _, err := w.Write(append(data, '\n')); err != nil {
				timer.Stop()
				shutdown(err)
				return
			}
			select {
			case <-completed:
			case <-ctx.Done():
				timer.Stop()
				return
			}
			if !timer.Stop() {
				shutdown(context.DeadlineExceeded)
				return
			}
			// Processing has ended. Only now does the original idle budget
			// resume; a 60-second Prepare cannot trigger a 30-second idle EOF.
			if err := conn.SetReadDeadline(time.Now().Add(limits.idle)); err != nil {
				shutdown(err)
				return
			}
		}
	}()
	runDone := make(chan error, 1)
	go func() { runDone <- run(r, responses, ownerUID) }()
	var runErr error
	select {
	case runErr = <-runDone:
		cause := context.Cause(ctx)
		cancel(runErr)
		closeInput()
		if runErr != nil {
			return runErr
		}
		if cause == io.EOF {
			return nil
		}
		return cause
	case <-ctx.Done():
		closeInput()
		// The root command exits when this function returns. Give normal host
		// Close a bounded opportunity to stop forwarding; a stuck operation
		// cannot keep utun descriptors alive indefinitely after broker failure.
		// PF and the manager's durable recovery journal survive process exit.
		timer := time.NewTimer(limits.cleanup)
		defer timer.Stop()
		select {
		case runErr = <-runDone:
		case <-timer.C:
			return ErrUnavailable
		}
	}
	_ = r.Close()
	_ = conn.Close()
	if runErr != nil {
		return runErr
	}
	if context.Cause(ctx) == io.EOF {
		return nil
	}
	return context.Cause(ctx)
}

func compactObject(data []byte) ([]byte, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' || control.ValidateJSON(data) != nil {
		return nil, ErrFrame
	}
	var b bytes.Buffer
	if err := json.Compact(&b, data); err != nil {
		return nil, ErrFrame
	}
	return b.Bytes(), nil
}

type responseWriter struct {
	mu        sync.Mutex
	conn      net.Conn
	buffer    []byte
	completed chan struct{}
	fail      func(error)
}

func (w *responseWriter) begin(deadline time.Time, completed chan struct{}) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.completed != nil || len(w.buffer) != 0 {
		return ErrFrame
	}
	if err := w.conn.SetDeadline(deadline); err != nil {
		return err
	}
	w.completed = completed
	return nil
}

func (w *responseWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	defer func() {
		if err != nil {
			w.fail(err)
		}
	}()
	n = len(p)
	for len(p) > 0 {
		if w.completed == nil {
			return 0, ErrFrame
		}
		end := bytes.IndexByte(p, '\n')
		part := p
		if end >= 0 {
			part = p[:end]
		}
		if len(w.buffer)+len(part) > MaxFrame {
			return 0, ErrFrame
		}
		w.buffer = append(w.buffer, part...)
		if end < 0 {
			break
		}
		data, err := compactObject(w.buffer)
		if err != nil {
			return 0, err
		}
		if err = WriteFrame(w.conn, data); err != nil {
			return 0, err
		}
		w.buffer = w.buffer[:0]
		w.completed <- struct{}{}
		w.completed = nil
		p = p[end+1:]
	}
	return n, nil
}
