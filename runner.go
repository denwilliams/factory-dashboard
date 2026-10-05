package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxStdout = 64 << 20

// limitedBuffer stops growing past max so a misbehaving CLI can't balloon memory.
type limitedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room < len(p) {
		l.truncated = true
		if room > 0 {
			l.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return l.buf.Write(p)
}

// runCLI runs a CLI tool and returns stdout. stdin may be nil.
func runCLI(ctx context.Context, timeout time.Duration, stdin io.Reader, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dataDir()
	cmd.Env = append(os.Environ(),
		"NO_COLOR=1",
		"GH_PROMPT_DISABLED=1",
		"GH_NO_UPDATE_NOTIFIER=1",
		"VERCEL_TELEMETRY_DISABLED=1",
		"NO_UPDATE_NOTIFIER=1",
	)
	cmd.Stdin = stdin
	out := &limitedBuffer{max: maxStdout}
	errBuf := &limitedBuffer{max: 8 << 10}
	cmd.Stdout = out
	cmd.Stderr = errBuf
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("%s timed out after %s", name, timeout)
	}
	if err != nil {
		var ee *exec.ExitError
		msg := strings.TrimSpace(errBuf.buf.String())
		if errors.As(err, &ee) && msg != "" {
			return nil, fmt.Errorf("%s %s: %s", name, firstArgs(args), lastLines(msg, 3))
		}
		return nil, fmt.Errorf("%s %s: %w", name, firstArgs(args), err)
	}
	if out.truncated {
		return nil, fmt.Errorf("%s output exceeded %d MB", name, maxStdout>>20)
	}
	return out.buf.Bytes(), nil
}

func firstArgs(args []string) string {
	if len(args) > 3 {
		args = args[:3]
	}
	return strings.Join(args, " ")
}

func lastLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

// extendPath makes the CLIs findable when launched by launchd/systemd, which
// start with a bare PATH.
func extendPath(extra []string) {
	uh, _ := os.UserHomeDir()
	candidates := append([]string{}, extra...)
	candidates = append(candidates,
		"/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin",
		filepath.Join(uh, ".local", "bin"),
		filepath.Join(uh, "go", "bin"),
		filepath.Join(uh, ".npm-global", "bin"),
		filepath.Join(uh, ".bun", "bin"),
		filepath.Join(uh, ".volta", "bin"),
	)
	have := map[string]bool{}
	parts := filepath.SplitList(os.Getenv("PATH"))
	for _, p := range parts {
		have[p] = true
	}
	for _, c := range candidates {
		if c == "" || have[c] {
			continue
		}
		if fi, err := os.Stat(c); err == nil && fi.IsDir() {
			parts = append(parts, c)
			have[c] = true
		}
	}
	os.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
}

func haveCLI(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// jsonStart skips any banner text a CLI prints before its JSON payload.
func jsonStart(b []byte) []byte {
	if i := bytes.IndexAny(b, "{["); i > 0 {
		return b[i:]
	}
	return b
}
