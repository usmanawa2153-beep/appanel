//go:build linux

// Package term starts an interactive shell on a pseudo-terminal using only the
// standard library.
package term

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"syscall"
	"unsafe"
)

// Session is a running shell attached to a PTY.
type Session struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

func ioctl(fd uintptr, req, arg uintptr) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg); e != 0 {
		return e
	}
	return nil
}

// Start launches the user's shell (or /bin/bash, then /bin/sh) with a PTY of
// the given size.
func Start(cols, rows uint16) (*Session, error) {
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open /dev/ptmx: %w", err)
	}
	fail := func(e error) (*Session, error) { ptmx.Close(); return nil, e }

	var unlock int32
	if err := ioctl(ptmx.Fd(), syscall.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); err != nil {
		return fail(fmt.Errorf("unlock pty: %w", err))
	}
	var n uint32
	if err := ioctl(ptmx.Fd(), syscall.TIOCGPTN, uintptr(unsafe.Pointer(&n))); err != nil {
		return fail(fmt.Errorf("pty number: %w", err))
	}
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return fail(fmt.Errorf("open pty slave: %w", err))
	}
	defer slave.Close() // the child keeps its own copies

	shell := pickShell()
	cmd := exec.Command(shell, "-l")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.Env = environment(shell)
	cmd.Dir = homeDir()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return fail(fmt.Errorf("start %s: %w", shell, err))
	}
	s := &Session{ptmx: ptmx, cmd: cmd}
	_ = s.Resize(cols, rows)
	go func() { _ = cmd.Wait() }() // reap the child; Read returns EIO once it exits
	return s, nil
}

func pickShell() string {
	for _, c := range []string{os.Getenv("SHELL"), "/bin/bash", "/bin/sh"} {
		if c == "" {
			continue
		}
		if st, err := os.Stat(c); err == nil && st.Mode().IsRegular() && st.Mode()&0o111 != 0 {
			return c
		}
	}
	return "/bin/sh"
}

func homeDir() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/"
}

func environment(shell string) []string {
	u, _ := user.Current()
	name := "root"
	if u != nil {
		name = u.Username
	}
	return []string{
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"LANG=C.UTF-8",
		"HOME=" + homeDir(),
		"USER=" + name,
		"LOGNAME=" + name,
		"SHELL=" + shell,
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
}

// Read reads shell output. It returns an error when the shell has exited.
func (s *Session) Read(p []byte) (int, error) { return s.ptmx.Read(p) }

// Write sends keystrokes to the shell.
func (s *Session) Write(p []byte) (int, error) { return s.ptmx.Write(p) }

// Resize changes the terminal size.
func (s *Session) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return errors.New("invalid size")
	}
	ws := struct{ Row, Col, X, Y uint16 }{Row: rows, Col: cols}
	return ioctl(s.ptmx.Fd(), syscall.TIOCSWINSZ, uintptr(unsafe.Pointer(&ws)))
}

// Close hangs up the terminal and kills the shell's whole process group.
func (s *Session) Close() {
	if s.cmd.Process != nil {
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGHUP)
		_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = s.ptmx.Close()
}
