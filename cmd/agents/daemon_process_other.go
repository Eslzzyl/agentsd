//go:build !windows && !darwin && !linux && !freebsd && !netbsd && !openbsd && !dragonfly && !solaris

package main

import "os/exec"

func configureDetachedCommand(_ *exec.Cmd) {}
