package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/usmanawa2153-beep/appanel/internal/term"
	"github.com/usmanawa2153-beep/appanel/internal/ws"
)

const (
	maxTerminals   = 5
	wsPingEvery    = 30 * time.Second
	wsReadDeadline = 90 * time.Second
)

var activeTerminals atomic.Int32

type resizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// handleTerminal bridges a WebSocket to a root shell on a PTY.
// Binary frames carry keystrokes in and shell output out; a text frame carries
// a JSON control message ({"type":"resize","cols":N,"rows":N}).
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	if !ws.SameOrigin(r) {
		writeErr(w, http.StatusForbidden, "cross-origin websocket refused")
		return
	}
	if activeTerminals.Add(1) > maxTerminals {
		activeTerminals.Add(-1)
		writeErr(w, http.StatusTooManyRequests, "too many open terminals")
		return
	}
	defer activeTerminals.Add(-1)

	conn, err := ws.Upgrade(w, r)
	if err != nil {
		return // Upgrade already replied
	}
	defer conn.Close()

	sh, err := term.Start(80, 24)
	if err != nil {
		logf("terminal start: %v", err)
		_ = conn.WriteText(`{"type":"error","message":"could not start a shell"}`)
		conn.CloseWith(1011)
		return
	}
	defer sh.Close()
	logf("terminal opened by %s from %s", userFrom(r).Username, clientIP(r))

	done := make(chan struct{})

	// shell -> browser
	go func() {
		defer close(done)
		buf := make([]byte, 16<<10)
		for {
			n, err := sh.Read(buf)
			if n > 0 {
				if werr := conn.WriteBinary(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = conn.WriteText(`{"type":"exit"}`)
				return
			}
		}
	}()

	// keepalive
	stopPing := make(chan struct{})
	defer close(stopPing)
	go func() {
		t := time.NewTicker(wsPingEvery)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				if conn.Ping() != nil {
					return
				}
			case <-stopPing:
				return
			case <-done:
				return
			}
		}
	}()

	// browser -> shell
	go func() {
		select {
		case <-done: // shell exited: unblock the read loop below
			conn.CloseWith(1000)
		case <-stopPing:
		}
	}()
	for {
		_ = conn.SetReadDeadline(time.Now().Add(wsReadDeadline))
		op, data, err := conn.ReadMessage()
		if err != nil {
			if !errors.Is(err, ws.ErrClosed) {
				logf("terminal closed: %v", err)
			}
			return
		}
		switch op {
		case ws.OpBinary:
			if _, err := sh.Write(data); err != nil {
				return
			}
		case ws.OpText:
			var m resizeMsg
			if json.Unmarshal(data, &m) == nil && m.Type == "resize" && m.Cols > 0 && m.Rows > 0 && m.Cols <= 1000 && m.Rows <= 500 {
				_ = sh.Resize(m.Cols, m.Rows)
			}
		}
	}
}
