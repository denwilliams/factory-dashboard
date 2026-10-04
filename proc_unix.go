//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// detachAttr puts the daemon in its own session so it survives the terminal closing.
func detachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

func terminate(p *os.Process) error { return p.Signal(syscall.SIGTERM) }

func processRSS(pid int) string {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "unknown"
	}
	kb, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return "unknown"
	}
	return fmt.Sprintf("%.1f MB RSS", float64(kb)/1024)
}
