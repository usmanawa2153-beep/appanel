// Package ws is a small RFC 6455 WebSocket server implementation (no
// extensions, no compression) so the panel stays dependency-free.
package ws

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // mandated by RFC 6455 for the handshake accept key
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const guid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcodes.
const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xA
)

// MaxMessage bounds a single (reassembled) message from the client.
const MaxMessage = 1 << 20

var (
	ErrClosed     = errors.New("websocket closed")
	ErrProtocol   = errors.New("websocket protocol error")
	ErrTooLarge   = errors.New("websocket message too large")
	errNotUpgrade = errors.New("not a websocket upgrade request")
)

// Conn is a server-side WebSocket connection. Reads must come from one
// goroutine; writes are serialised internally and may come from many.
type Conn struct {
	c    net.Conn
	br   *bufio.Reader
	wmu  sync.Mutex
	done bool
}

// SameOrigin reports whether the request's Origin (when present) matches its
// Host. Browsers always send Origin on WebSocket handshakes, so this blocks
// cross-site WebSocket hijacking.
func SameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true // non-browser client; the session cookie still has to be valid
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// Upgrade performs the handshake. On error it has already written an HTTP
// error response.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if r.Method != http.MethodGet ||
		!headerHas(r.Header, "Connection", "upgrade") ||
		!headerHas(r.Header, "Upgrade", "websocket") {
		http.Error(w, "websocket upgrade required", http.StatusUpgradeRequired)
		return nil, errNotUpgrade
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "unsupported websocket version", http.StatusBadRequest)
		return nil, errNotUpgrade
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if raw, err := base64.StdEncoding.DecodeString(key); err != nil || len(raw) != 16 {
		http.Error(w, "bad Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, errNotUpgrade
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "websocket unsupported", http.StatusInternalServerError)
		return nil, errNotUpgrade
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + guid)) //nolint:gosec
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"
	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write([]byte(resp)); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetWriteDeadline(time.Time{})
	return &Conn{c: conn, br: brw.Reader}, nil
}

func headerHas(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, p := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(p), token) {
				return true
			}
		}
	}
	return false
}

// SetReadDeadline forwards to the underlying connection.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.c.SetReadDeadline(t) }

// Close closes the TCP connection.
func (c *Conn) Close() error { return c.c.Close() }

// ReadMessage returns the next text or binary message, answering pings and
// handling close frames transparently.
func (c *Conn) ReadMessage() (op int, data []byte, err error) {
	var msg []byte
	msgOp := -1
	for {
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch opcode {
		case OpPing:
			if err := c.write(OpPong, payload); err != nil {
				return 0, nil, err
			}
		case OpPong:
		case OpClose:
			_ = c.write(OpClose, payload)
			return 0, nil, ErrClosed
		case OpText, OpBinary:
			if msgOp != -1 {
				return 0, nil, ErrProtocol
			}
			msgOp, msg = opcode, payload
			if fin {
				return msgOp, msg, nil
			}
		case OpContinuation:
			if msgOp == -1 {
				return 0, nil, ErrProtocol
			}
			if len(msg)+len(payload) > MaxMessage {
				return 0, nil, ErrTooLarge
			}
			msg = append(msg, payload...)
			if fin {
				return msgOp, msg, nil
			}
		default:
			return 0, nil, ErrProtocol
		}
	}
}

func (c *Conn) readFrame() (fin bool, opcode int, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(c.br, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	if h[0]&0x70 != 0 { // RSV bits: no extensions negotiated
		err = ErrProtocol
		return
	}
	opcode = int(h[0] & 0x0f)
	if h[1]&0x80 == 0 { // clients must mask
		err = ErrProtocol
		return
	}
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(c.br, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if opcode >= 0x8 && (n > 125 || !fin) { // control frames: short and unfragmented
		err = ErrProtocol
		return
	}
	if n > MaxMessage {
		err = ErrTooLarge
		return
	}
	var mask [4]byte
	if _, err = io.ReadFull(c.br, mask[:]); err != nil {
		return
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	for i := range payload {
		payload[i] ^= mask[i&3]
	}
	return
}

func (c *Conn) write(opcode int, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.done {
		return ErrClosed
	}
	hdr := make([]byte, 0, 10)
	hdr = append(hdr, 0x80|byte(opcode))
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 0xffff:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	_ = c.c.SetWriteDeadline(time.Now().Add(15 * time.Second))
	if _, err := c.c.Write(hdr); err != nil {
		c.done = true
		return err
	}
	if _, err := c.c.Write(payload); err != nil {
		c.done = true
		return err
	}
	return nil
}

// WriteBinary and WriteText send one complete message.
func (c *Conn) WriteBinary(b []byte) error { return c.write(OpBinary, b) }
func (c *Conn) WriteText(s string) error   { return c.write(OpText, []byte(s)) }

// Ping sends a ping frame (keeps idle connections alive through proxies).
func (c *Conn) Ping() error { return c.write(OpPing, nil) }

// CloseWith sends a close frame with a status code and then closes the socket.
func (c *Conn) CloseWith(code uint16) {
	_ = c.write(OpClose, []byte{byte(code >> 8), byte(code)})
	c.wmu.Lock()
	c.done = true
	c.wmu.Unlock()
	_ = c.c.Close()
}
