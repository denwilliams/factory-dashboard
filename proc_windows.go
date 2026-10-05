//go:build windows

package main

import (
	"os"
	"syscall"
)

const createNewProcessGroup = 0x00000200
const detachedProcess = 0x00000008

func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
}

func terminate(p *os.Process) error { return p.Kill() }

func processRSS(int) string { return "unknown" }
