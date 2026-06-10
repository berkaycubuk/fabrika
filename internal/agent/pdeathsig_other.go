//go:build !linux

package agent

import "syscall"

func setPdeathsig(_ *syscall.SysProcAttr) {}
