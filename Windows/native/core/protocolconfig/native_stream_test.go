package protocolconfig

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func nativeSOCKSOutbound(t *testing.T) *Outbound {
	t.Helper()
	entry := nativeXrayServer(t, "socks", map[string]any{"auth": "password", "accounts": []any{map[string]any{"user": "fixture", "pass": "fixture-password"}}, "udp": true, "ip": "127.0.0.1"})
	host, port, _ := net.SplitHostPort(entry)
	n, _ := strconv.Atoi(port)
	return nativeFixtureOutbound(t, context.Background(), "socks", map[string]any{"address": host, "port": n, "user": "fixture", "pass": "fixture-password"})
}

func nativeTCPReply(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var accepted net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := l.Accept()
		if err != nil {
			return
		}
		mu.Lock()
		accepted = c
		mu.Unlock()
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		serve(c)
	}()
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		if accepted != nil {
			_ = accepted.Close()
		}
		mu.Unlock()
		nativeAwait(t, done, "reply fixture")
	})
	return l.Addr().String()
}

func TestNativeHTTPResponseNormalEOF(t *testing.T) {
	for _, length := range []bool{true, false} {
		t.Run(fmt.Sprint("content-length=", length), func(t *testing.T) {
			target := nativeTCPReply(t, func(c net.Conn) {
				if _, err := http.ReadRequest(bufio.NewReader(c)); err != nil {
					return
				}
				header := "HTTP/1.1 200 OK\r\nConnection: close\r\n"
				if length {
					header += "Content-Length: 5\r\n"
				}
				_, _ = io.WriteString(c, header+"\r\nhello")
			})
			out := nativeSOCKSOutbound(t)
			tr := &http.Transport{Proxy: nil, DialContext: out.DialContext, DisableKeepAlives: true}
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
			response, err := client.Get("http://" + target + "/")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if string(body) != "hello" || err != nil {
				t.Fatalf("complete HTTP body=%q err=%v", body, err)
			}
		})
	}
}

func TestNativeCloseWriteDrainsDelayedResponse(t *testing.T) {
	target := nativeTCPReply(t, func(c net.Conn) {
		request := make([]byte, 7)
		if _, err := io.ReadFull(c, request); err != nil || string(request) != "request" {
			return
		}
		_, _ = io.WriteString(c, "ready")
		// This application does not wait for upstream FIN: SOCKS itself cannot
		// forward physical half-close. A complete request still deserves its reply.
		time.Sleep(75 * time.Millisecond)
		_, _ = io.WriteString(c, "delayed-response")
	})
	out := nativeSOCKSOutbound(t)
	c, err := out.DialContext(context.Background(), "tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err = io.WriteString(c, "request"); err != nil {
		t.Fatal(err)
	}
	ready := make([]byte, 5)
	if _, err = io.ReadFull(c, ready); err != nil || string(ready) != "ready" {
		t.Fatalf("ack=%q err=%v", ready, err)
	}
	half, ok := c.(interface{ CloseWrite() error })
	if !ok {
		t.Fatal("native stream cannot close upload without truncating download")
	}
	if err = half.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if err = half.CloseWrite(); err != nil {
		t.Fatalf("repeat CloseWrite: %v", err)
	}
	if _, err = c.Write([]byte("must-not-send")); err == nil {
		t.Fatal("write after CloseWrite succeeded")
	}
	response, err := io.ReadAll(c)
	if string(response) != "delayed-response" || err != nil {
		t.Fatalf("delayed response=%q err=%v", response, err)
	}
}

func TestNativeCloseWriteBoundsDrainAndSessionCancellation(t *testing.T) {
	for _, retire := range []string{"drain-timeout", "session-cancel"} {
		t.Run(retire, func(t *testing.T) {
			out := nativeSOCKSOutbound(t)
			c, err := out.DialContext(context.Background(), "tcp", nativeEchoTCP(t))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			if _, err = io.WriteString(c, "ready"); err != nil {
				t.Fatal(err)
			}
			if _, err = io.ReadFull(c, make([]byte, 5)); err != nil {
				t.Fatal(err)
			}
			_ = c.SetDeadline(time.Time{})
			if retire == "drain-timeout" {
				// Exercise the same drain mechanism with a short private test budget.
				err = c.(*nativeStream).closeWrite(40 * time.Millisecond)
			} else {
				err = c.(interface{ CloseWrite() error }).CloseWrite()
			}
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			var readErr error
			go func() { defer close(done); _, readErr = c.Read(make([]byte, 1)) }()
			if retire == "session-cancel" {
				out.cancel()
			}
			nativeAwait(t, done, "half-closed download retirement")
			want := error(net.ErrClosed)
			if retire == "drain-timeout" {
				want = os.ErrDeadlineExceeded
			}
			if !errors.Is(readErr, want) {
				t.Fatalf("incomplete download reported successful EOF: %v", readErr)
			}
			if err = out.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeCloseWriteIdleDrainRenewsOnDeliveredBytes(t *testing.T) {
	begin, stop := make(chan struct{}), make(chan struct{})
	defer close(stop)
	target := nativeTCPReply(t, func(c net.Conn) {
		if _, err := io.ReadFull(c, make([]byte, 7)); err != nil {
			return
		}
		_, _ = io.WriteString(c, "ready")
		select {
		case <-begin:
		case <-stop:
			return
		}
		for range 8 {
			time.Sleep(30 * time.Millisecond)
			if _, err := io.WriteString(c, "tick"); err != nil {
				return
			}
		}
		<-stop
	})
	out := nativeSOCKSOutbound(t)
	c, err := out.DialContext(context.Background(), "tcp", target)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = io.WriteString(c, "request"); err != nil {
		t.Fatal(err)
	}
	if _, err = io.ReadFull(c, make([]byte, 5)); err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Time{})
	const idle = 150 * time.Millisecond
	started := time.Now()
	if err = c.(*nativeStream).closeWrite(idle); err != nil {
		t.Fatal(err)
	}
	close(begin)
	got := make([]byte, 8*len("tick"))
	if _, err = io.ReadFull(c, got); err != nil || string(got) != strings.Repeat("tick", 8) {
		t.Fatalf("active drain truncated: %q %v", got, err)
	}
	if time.Since(started) <= idle {
		t.Fatal("fixture did not exceed one idle timeout window")
	}
	done := make(chan struct{})
	var readErr error
	go func() { defer close(done); _, readErr = c.Read(make([]byte, 1)) }()
	nativeAwait(t, done, "idle drain after active response")
	if !errors.Is(readErr, os.ErrDeadlineExceeded) {
		t.Fatalf("idle drain termination: %v", readErr)
	}
}

func TestNativeConcurrentCloseWriteAndRetirement(t *testing.T) {
	out := nativeSOCKSOutbound(t)
	c, err := out.DialContext(context.Background(), "tcp", nativeEchoTCP(t))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_ = c.(interface{ CloseWrite() error }).CloseWrite()
			_ = c.SetDeadline(time.Now().Add(time.Second))
			_ = c.Close()
		})
	}
	wg.Wait()
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
