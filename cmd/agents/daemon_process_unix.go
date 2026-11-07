//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly || solaris

package main

import (
	"os/exec"
	"syscall"
)

func configureDetachedCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
