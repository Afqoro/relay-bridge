package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"time"
)

// SOCKS5 server (RFC 1928). Only the CONNECT command is handled, and only for
// plain HTTP targets (port 80): because the remote relay is HTTP-only, a raw
// TCP tunnel (needed for HTTPS and arbitrary protocols) cannot be built. For
// port 80 we confirm the connection, then forward the client's origin-form HTTP
// request through the relay.
//
// Auth method: no-auth (0x00) only.

func serveSOCKS5(l net.Listener, pool *RelayPool) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		go handleSOCKS5(conn, pool)
	}
}

func handleSOCKS5(conn net.Conn, pool *RelayPool) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Minute))
	br := bufio.NewReader(conn)

	// GREETING: VER NMETHODS METHODS
	if err := socksReadGreeting(br); err != nil {
		return
	}
	// select no-auth (0x00)
	if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// REQUEST: VER CMD RSV ATYP ADDR PORT
	head := make([]byte, 4)
	if _, err := io.ReadFull(br, head); err != nil {
		return
	}
	ver, cmd, atyp := head[0], head[1], head[3]
	if ver != 0x05 || cmd != 0x01 { // SOCKS5 + CONNECT only
		replySOCKS5Err(conn, 0x07) // command not supported
		return
	}

	host, err := socksReadAddr(br, atyp)
	if err != nil {
		replySOCKS5Err(conn, 0x08) // address type not supported
		return
	}
	portB := make([]byte, 2)
	if _, err := io.ReadFull(br, portB); err != nil {
		return
	}
	port := int(binary.BigEndian.Uint16(portB))

	// Only plain HTTP (port 80) can be bridged through an HTTP-only relay.
	if port != 80 {
		replySOCKS5Err(conn, 0x07) // command not supported
		return
	}

	// Success (BND.ADDR = 0.0.0.0:0)
	if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Minute))
	serveHTTPOverConn(conn, host, "http", pool)
}

func socksReadGreeting(br *bufio.Reader) error {
	var ver, n byte
	if err := readByte(br, &ver); err != nil {
		return err
	}
	if err := readByte(br, &n); err != nil {
		return err
	}
	methods := make([]byte, n)
	if _, err := io.ReadFull(br, methods); err != nil {
		return err
	}
	return nil
}

func socksReadAddr(br *bufio.Reader, atyp byte) (string, error) {
	switch atyp {
	case 0x01: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	case 0x03: // domain name
		n, err := br.ReadByte()
		if err != nil {
			return "", err
		}
		d := make([]byte, n)
		if _, err := io.ReadFull(br, d); err != nil {
			return "", err
		}
		return string(d), nil
	case 0x04: // IPv6
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		return net.IP(b).String(), nil
	default:
		return "", io.ErrUnexpectedEOF
	}
}

func replySOCKS5Err(conn net.Conn, rep byte) {
	_, _ = conn.Write([]byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

func readByte(br *bufio.Reader, dst *byte) error {
	b, err := br.ReadByte()
	*dst = b
	return err
}
