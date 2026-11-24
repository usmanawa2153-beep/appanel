package ws

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func maskedFrame(fin bool, op byte, payload []byte) []byte {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	f := []byte{b0}
	switch n := len(payload); {
	case n < 126:
		f = append(f, 0x80|byte(n))
	case n <= 0xffff:
		f = append(f, 0x80|126, byte(n>>8), byte(n))
	default:
		f = append(f, 0x80|127, 0, 0, 0, 0, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}
	mask := []byte{9, 8, 7, 6}
	f = append(f, mask...)
	for i, c := range payload {
		f = append(f, c^mask[i&3])
	}
	return f
}

// pair returns a server-side Conn plus the raw client end of the pipe.
func pair(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	srv, cli := net.Pipe()
	t.Cleanup(func() { srv.Close(); cli.Close() })
	return &Conn{c: srv, br: bufio.NewReader(srv)}, cli
}

func read(t *testing.T, c *Conn) (int, []byte, error) {
	t.Helper()
	type res struct {
		op   int
		data []byte
		err  error
	}
	ch := make(chan res, 1)
	go func() { op, d, err := c.ReadMessage(); ch <- res{op, d, err} }()
	select {
	case r := <-ch:
		return r.op, r.data, r.err
	case <-time.After(3 * time.Second):
		t.Fatal("ReadMessage timed out")
		return 0, nil, nil
	}
}

func TestReadsMaskedTextAndBinary(t *testing.T) {
	c, cli := pair(t)
	go cli.Write(maskedFrame(true, OpText, []byte("hello")))
	op, d, err := read(t, c)
	if err != nil || op != OpText || string(d) != "hello" {
		t.Fatalf("got %d %q %v", op, d, err)
	}
}

func TestFragmentedMessageIsReassembled(t *testing.T) {
	c, cli := pair(t)
	go func() {
		cli.Write(maskedFrame(false, OpText, []byte("foo")))
		cli.Write(maskedFrame(false, OpContinuation, []byte("bar")))
		cli.Write(maskedFrame(true, OpContinuation, []byte("baz")))
	}()
	op, d, err := read(t, c)
	if err != nil || op != OpText || string(d) != "foobarbaz" {
		t.Fatalf("got %d %q %v", op, d, err)
	}
}

func TestUnmaskedClientFrameRejected(t *testing.T) {
	c, cli := pair(t)
	go cli.Write([]byte{0x81, 0x02, 'h', 'i'}) // no mask bit
	if _, _, err := read(t, c); !errors.Is(err, ErrProtocol) {
		t.Fatalf("want ErrProtocol, got %v", err)
	}
}

func TestOversizeMessageRejected(t *testing.T) {
	c, cli := pair(t)
	go cli.Write([]byte{0x82, 0x80 | 127, 0, 0, 0, 0, 0, 0x20, 0, 0}) // 2 MiB length header
	if _, _, err := read(t, c); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestReservedBitsAndBadContinuationRejected(t *testing.T) {
	c, cli := pair(t)
	go cli.Write([]byte{0xC1, 0x80, 1, 2, 3, 4}) // RSV1 set
	if _, _, err := read(t, c); !errors.Is(err, ErrProtocol) {
		t.Fatalf("RSV: got %v", err)
	}
	c2, cli2 := pair(t)
	go cli2.Write(maskedFrame(true, OpContinuation, []byte("x"))) // continuation with nothing to continue
	if _, _, err := read(t, c2); !errors.Is(err, ErrProtocol) {
		t.Fatalf("orphan continuation: got %v", err)
	}
}

func TestPingGetsPongThenMessageDelivered(t *testing.T) {
	c, cli := pair(t)
	got := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 2+4)
		io.ReadFull(cli, buf) // server's pong: 0x8A len=4 "ping"
		got <- buf
	}()
	go func() {
		cli.Write(maskedFrame(true, OpPing, []byte("ping")))
		cli.Write(maskedFrame(true, OpBinary, []byte{1, 2, 3}))
	}()
	op, d, err := read(t, c)
	if err != nil || op != OpBinary || !bytes.Equal(d, []byte{1, 2, 3}) {
		t.Fatalf("got %d %v %v", op, d, err)
	}
	select {
	case p := <-got:
		if p[0] != 0x80|OpPong || p[1] != 4 || string(p[2:]) != "ping" {
			t.Fatalf("bad pong %v", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no pong")
	}
}

func TestCloseFrameEndsRead(t *testing.T) {
	c, cli := pair(t)
	go io.Copy(io.Discard, cli)
	go cli.Write(maskedFrame(true, OpClose, []byte{0x03, 0xe8}))
	if _, _, err := read(t, c); !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed, got %v", err)
	}
}

func TestSameOrigin(t *testing.T) {
	mk := func(origin, host string) *http.Request {
		r, _ := http.NewRequest("GET", "http://"+host+"/", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	cases := []struct {
		origin, host string
		want         bool
	}{
		{"", "panel:8443", true},
		{"https://panel:8443", "panel:8443", true},
		{"https://PANEL:8443", "panel:8443", true},
		{"https://evil.example", "panel:8443", false},
		{"https://panel:9999", "panel:8443", false},
		{"null", "panel:8443", false},
	}
	for _, c := range cases {
		if got := SameOrigin(mk(c.origin, c.host)); got != c.want {
			t.Errorf("origin %q host %q = %v, want %v", c.origin, c.host, got, c.want)
		}
	}
}
