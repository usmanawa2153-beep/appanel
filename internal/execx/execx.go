// Package execx runs external commands behind an interface so the code that
// drives system tools (ufw, mysql, tar, ...) can be tested with a fake.
package execx

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
)

// Runner executes a command and returns its combined stdout+stderr.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
	// RunInput is like Run but feeds input to the command's stdin.
	RunInput(ctx context.Context, input, name string, args ...string) (string, error)
}

// Exec is the real Runner.
type Exec struct{}

func (Exec) Run(ctx context.Context, name string, args ...string) (string, error) {
	return Exec{}.RunInput(ctx, "", name, args...)
}

func (Exec) RunInput(ctx context.Context, input, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive", "LC_ALL=C")
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return out.String(), err
}

// Tail returns at most the last n bytes of s, trimmed, for showing in the UI.
func Tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "…" + s[len(s)-n:]
	}
	return s
}
