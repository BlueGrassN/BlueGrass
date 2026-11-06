//go:build windows

package serviceipc

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const PipeName = `\\.\pipe\BlueGrass.Control`

// Do not grant FILE_CREATE_PIPE_INSTANCE (0x4) to interactive users. The
// library also creates the first instance exclusively and rejects remote clients.
const PipeSDDL = "D:P(D;;GA;;;NU)(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x12019B;;;IU)"

type Signer func(path string) (string, error)

func Listen() (net.Listener, error) {
	return winio.ListenPipe(PipeName, &winio.PipeConfig{SecurityDescriptor: PipeSDDL, InputBufferSize: MaxFrame, OutputBufferSize: MaxFrame})
}

func processIdentity(pid uint32, signer Signer) (Peer, uint32, error) {
	var peer Peer
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return peer, 0, ErrPeer
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token) != nil {
		return peer, 0, ErrPeer
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return peer, 0, ErrPeer
	}
	var session, size uint32
	if windows.GetTokenInformation(token, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), 4, &size) != nil {
		return peer, 0, ErrPeer
	}
	buffer := make([]uint16, 32768)
	n := uint32(len(buffer))
	if windows.QueryFullProcessImageName(h, 0, &buffer[0], &n) != nil {
		return peer, 0, ErrPeer
	}
	path := windows.UTF16ToString(buffer[:n])
	// Resolve junctions before comparing the exact installed filename.
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return peer, 0, ErrPeer
	}
	publisher, err := signer(path)
	if err != nil || publisher == "" {
		return peer, 0, ErrPeer
	}
	var info windows.PROCESS_BASIC_INFORMATION
	if windows.NtQueryInformationProcess(h, windows.ProcessBasicInformation, unsafe.Pointer(&info), uint32(unsafe.Sizeof(info)), nil) != nil {
		return peer, 0, ErrPeer
	}
	peer = Peer{Identity: Identity{SID: user.User.Sid.String(), Session: session, PID: pid}, Image: path, Signer: publisher}
	return peer, uint32(info.InheritedFromUniqueProcessId), nil
}

// Authenticate validates the actual connected process, never identities supplied
// in JSON. server=true means the caller is a broker validating the service.
func Authenticate(conn net.Conn, installedDir, trustedSigner string, signer Signer, server bool) (Identity, error) {
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok || signer == nil {
		return Identity{}, ErrPeer
	}
	var pid uint32
	var err error
	if server {
		err = windows.GetNamedPipeServerProcessId(windows.Handle(fd.Fd()), &pid)
	} else {
		err = windows.GetNamedPipeClientProcessId(windows.Handle(fd.Fd()), &pid)
	}
	if err != nil {
		return Identity{}, ErrPeer
	}
	peer, parentPID, err := processIdentity(pid, signer)
	if err != nil {
		return Identity{}, err
	}
	if !server {
		parent, _, err := processIdentity(parentPID, signer)
		if err != nil {
			return Identity{}, err
		}
		peer.ParentImage, peer.ParentSigner, peer.ParentSID, peer.ParentSession = parent.Image, parent.Signer, parent.SID, parent.Session
	}
	root, err := filepath.EvalSymlinks(installedDir)
	if err != nil {
		return Identity{}, ErrPeer
	}
	if ValidatePeer(peer, root, trustedSigner, server) != nil {
		return Identity{}, ErrPeer
	}
	return peer.Identity, nil
}

type Client struct {
	mu   sync.Mutex
	conn net.Conn
}

func Dial(ctx context.Context, installedDir, trustedSigner string, signer Signer) (*Client, error) {
	conn, err := winio.DialPipeContext(ctx, PipeName)
	if err != nil {
		return nil, errors.New("SERVICE_UNAVAILABLE")
	}
	if _, err = Authenticate(conn, installedDir, trustedSigner, signer, true); err != nil {
		conn.Close()
		return nil, err
	}
	return &Client{conn: conn}, nil
}
func (c *Client) Exchange(data []byte) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil, errors.New("SERVICE_CLOSED")
	}
	if err := c.conn.SetDeadline(time.Now().Add(45 * time.Second)); err != nil {
		return nil, err
	}
	if err := WriteFrame(c.conn, data); err != nil {
		return nil, err
	}
	return ReadFrame(c.conn)
}
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}
