package diagnostics

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"time"
)

func portNumber(port string) int { n, _ := strconv.Atoi(port); return n }

// loopbackDial never negotiates credentials, starts an engine, or dials the
// logical target itself. All target traffic uses the existing local gateway.
func loopbackDial(proxy LoopbackProxy) DialFunc {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" && network != "udp" || proxy.Kind == "http_connect" && network != "tcp" {
			return nil, errInvalid
		}
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", proxy.Address)
		if err != nil {
			return nil, errInvalid
		}
		ok := false
		defer func() {
			if !ok {
				conn.Close()
			}
		}()
		stop := context.AfterFunc(ctx, func() { conn.Close() })
		defer stop()
		deadline, hasDeadline := ctx.Deadline()
		if !hasDeadline {
			return nil, errInvalid
		}
		if conn.SetDeadline(deadline) != nil {
			return nil, errInvalid
		}
		if proxy.Kind == "http_connect" {
			tunnel, err := httpTunnel(conn, address)
			if err != nil {
				return nil, errInvalid
			}
			ok = true
			return tunnel, nil
		}
		if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
			return nil, errInvalid
		}
		var greeting [2]byte
		if _, err := io.ReadFull(conn, greeting[:]); err != nil || greeting != [2]byte{5, 0} {
			return nil, errInvalid
		}
		command := byte(1)
		target := address
		if network == "udp" {
			command = 3
			target = "0.0.0.0:0"
		}
		encoded, err := socksAddress(target)
		if err != nil {
			return nil, errInvalid
		}
		if _, err := conn.Write(append([]byte{5, command, 0}, encoded...)); err != nil {
			return nil, errInvalid
		}
		var header [3]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil || header != [3]byte{5, 0, 0} {
			return nil, errInvalid
		}
		bound, err := readSocksAddress(conn)
		if err != nil {
			return nil, errInvalid
		}
		if network == "tcp" {
			ok = true
			return conn, nil
		}
		// The UDP relay must belong to the same loopback gateway. A SOCKS
		// response cannot turn this into a remote/direct UDP socket.
		proxyHost, _, _ := net.SplitHostPort(proxy.Address)
		relayHost, relayPort, err := net.SplitHostPort(bound)
		if err != nil || !validPort(relayPort) {
			return nil, errInvalid
		}
		relayIP, err := netip.ParseAddr(relayHost)
		if err != nil {
			return nil, errInvalid
		}
		if relayIP.IsUnspecified() {
			relayHost = proxyHost
			relayIP = netip.MustParseAddr(proxyHost)
		}
		if !relayIP.IsLoopback() || relayIP.Unmap() != netip.MustParseAddr(proxyHost).Unmap() {
			return nil, errInvalid
		}
		udp, err := (&net.Dialer{}).DialContext(ctx, "udp", net.JoinHostPort(relayHost, relayPort))
		if err != nil {
			return nil, errInvalid
		}
		encoded, err = socksAddress(address)
		if err != nil {
			udp.Close()
			return nil, errInvalid
		}
		// UDP ASSOCIATE is valid only while its TCP control connection lives.
		// No application data is expected on that connection after the reply.
		go func() {
			var unexpected [1]byte
			conn.Read(unexpected[:])
			udp.Close()
		}()
		ok = true
		return &socksDatagram{Conn: udp, control: conn, header: append([]byte{0, 0, 0}, encoded...)}, nil
	}
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

func httpTunnel(conn net.Conn, address string) (net.Conn, error) {
	if _, err := io.WriteString(conn, "CONNECT "+address+" HTTP/1.1\r\nHost: "+address+"\r\n\r\n"); err != nil {
		return nil, errInvalid
	}
	reader := bufio.NewReaderSize(conn, 8192)
	var header []byte
	for {
		line, err := reader.ReadSlice('\n')
		if err != nil || len(header)+len(line) > 8192 {
			return nil, errInvalid
		}
		header = append(header, line...)
		if bytes.Equal(line, []byte("\r\n")) {
			break
		}
	}
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(header)), &http.Request{Method: http.MethodConnect})
	if err != nil || response.StatusCode != 200 {
		return nil, errInvalid
	}
	return &bufferedConn{Conn: conn, reader: reader}, nil
}

func socksAddress(address string) ([]byte, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errInvalid
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" {
		return nil, errInvalid
	}
	var result []byte
	if ip.Is4() {
		result = append([]byte{1}, ip.AsSlice()...)
	} else {
		result = append([]byte{4}, ip.AsSlice()...)
	}
	result = binary.BigEndian.AppendUint16(result, uint16(portNumber(port)))
	return result, nil
}

func readSocksAddress(reader io.Reader) (string, error) {
	var kind [1]byte
	if _, err := io.ReadFull(reader, kind[:]); err != nil {
		return "", errInvalid
	}
	length := 0
	switch kind[0] {
	case 1:
		length = 4
	case 4:
		length = 16
	default:
		return "", errInvalid
	}
	data := make([]byte, length+2)
	if _, err := io.ReadFull(reader, data); err != nil {
		return "", errInvalid
	}
	ip, ok := netip.AddrFromSlice(data[:length])
	if !ok {
		return "", errInvalid
	}
	return net.JoinHostPort(ip.String(), strconv.Itoa(int(binary.BigEndian.Uint16(data[length:])))), nil
}

type socksDatagram struct {
	net.Conn
	control net.Conn
	header  []byte
}

func (c *socksDatagram) Close() error { c.control.Close(); return c.Conn.Close() }
func (c *socksDatagram) SetDeadline(t time.Time) error {
	if err := c.control.SetDeadline(t); err != nil {
		return err
	}
	return c.Conn.SetDeadline(t)
}
func (c *socksDatagram) Write(p []byte) (int, error) {
	if len(p) > 512 {
		return 0, errInvalid
	}
	packet := append(append([]byte{}, c.header...), p...)
	n, err := c.Conn.Write(packet)
	if err != nil || n != len(packet) {
		return 0, errInvalid
	}
	return len(p), nil
}
func (c *socksDatagram) Read(p []byte) (int, error) {
	// One bounded datagram; reject fragments, foreign logical sources and
	// oversized packets instead of accepting a partial nonce match.
	packet := make([]byte, 1024)
	n, err := c.Conn.Read(packet)
	if err != nil {
		return 0, errInvalid
	}
	if n < len(c.header) || !bytes.Equal(packet[:len(c.header)], c.header) || n-len(c.header) > len(p) {
		return 0, errInvalid
	}
	return copy(p, packet[len(c.header):n]), nil
}
