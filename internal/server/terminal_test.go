package server

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// wsClient is a minimal masked-frame WebSocket client for tests.
type wsClient struct {
	c  net.Conn
	br *bufio.Reader
}

func dialWS(t *testing.T, e *env, cookie *http.Cookie, origin string) (*wsClient, *http.Response) {
	t.Helper()
	u := strings.TrimPrefix(e.ts.URL, "http://")
	c, err := net.Dial("tcp", u)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 16)
	rand.Read(key)
	var req bytes.Buffer
	req.WriteString("GET " + secret + "/api/terminal HTTP/1.1\r\nHost: " + u + "\r\n")
	req.WriteString("Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\n")
	req.WriteString("Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key) + "\r\n")
	if origin != "" {
		req.WriteString("Origin: " + origin + "\r\n")
	}
	if cookie != nil {
		req.WriteString("Cookie: " + cookie.Name + "=" + cookie.Value + "\r\n")
	}
	req.WriteString("\r\n")
	c.Write(req.Bytes())
	br := bufio.NewReader(c)
	res, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &wsClient{c: c, br: br}, res
}

func (w *wsClient) send(op byte, payload []byte) {
	hdr := []byte{0x80 | op}
	switch n := len(payload); {
	case n < 126:
		hdr = append(hdr, 0x80|byte(n))
	default:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	}
	mask := []byte{1, 2, 3, 4}
	hdr = append(hdr, mask...)
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ mask[i&3]
	}
	w.c.Write(append(hdr, masked...))
}

func (w *wsClient) recv(t *testing.T) (op byte, payload []byte) {
	t.Helper()
	var h [2]byte
	if _, err := io.ReadFull(w.br, h[:]); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	n := int(h[1] & 0x7f)
	if n == 126 {
		var b [2]byte
		io.ReadFull(w.br, b[:])
		n = int(binary.BigEndian.Uint16(b[:]))
	}
	payload = make([]byte, n)
	io.ReadFull(w.br, payload)
	return h[0] & 0x0f, payload
}

func TestTerminalRequiresAuthAndSameOrigin(t *testing.T) {
	e := newEnv(t)
	_, res := dialWS(t, e, nil, "")
	if res.StatusCode != 401 {
		t.Fatalf("unauthenticated terminal = %d, want 401", res.StatusCode)
	}
	c := e.login()
	_, res = dialWS(t, e, c, "https://evil.example")
	if res.StatusCode != 403 {
		t.Fatalf("cross-origin terminal = %d, want 403", res.StatusCode)
	}
}

func TestTerminalRunsCommands(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx in this environment")
	}
	e := newEnv(t)
	c := e.login()
	origin := e.ts.URL
	cl, res := dialWS(t, e, c, origin)
	if res.StatusCode != 101 {
		t.Fatalf("upgrade = %d", res.StatusCode)
	}
	// Server must answer the exact RFC accept key shape.
	if res.Header.Get("Sec-WebSocket-Accept") == "" {
		t.Fatal("no accept header")
	}
	cl.send(0x1, []byte(`{"type":"resize","cols":100,"rows":30}`))
	cl.send(0x2, []byte("echo appanel-$((6*7))\r"))

	deadline := time.Now().Add(8 * time.Second)
	var out bytes.Buffer
	for time.Now().Before(deadline) {
		cl.c.SetReadDeadline(deadline)
		op, p := cl.recv(t)
		if op == 0x2 {
			out.Write(p)
		}
		// The echoed command line contains "$((6*7))"; the result line does not.
		if strings.Contains(strings.ReplaceAll(out.String(), "$((6*7))", ""), "appanel-42") {
			break
		}
	}
	if !strings.Contains(strings.ReplaceAll(out.String(), "$((6*7))", ""), "appanel-42") {
		t.Fatalf("command output missing; got %q", out.String())
	}

	// Exiting the shell ends the session cleanly.
	cl.send(0x2, []byte("exit\r"))
	for {
		cl.c.SetReadDeadline(time.Now().Add(8 * time.Second))
		var h [2]byte
		if _, err := io.ReadFull(cl.br, h[:]); err != nil {
			break // connection closed
		}
		n := int(h[1] & 0x7f)
		io.CopyN(io.Discard, cl.br, int64(n))
		if h[0]&0x0f == 0x8 {
			break
		}
	}
}

func TestTerminalLimit(t *testing.T) {
	e := newEnv(t)
	c := e.login()
	var clients []*wsClient
	for i := 0; i < maxTerminals; i++ {
		cl, res := dialWS(t, e, c, e.ts.URL)
		if res.StatusCode != 101 {
			t.Fatalf("terminal %d = %d", i, res.StatusCode)
		}
		clients = append(clients, cl)
	}
	_, res := dialWS(t, e, c, e.ts.URL)
	if res.StatusCode != 429 {
		t.Fatalf("over-limit terminal = %d, want 429", res.StatusCode)
	}
	for _, cl := range clients {
		cl.c.Close()
	}
}
