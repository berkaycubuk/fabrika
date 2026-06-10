//go:build linux

package agent

import "syscall"

func setPdeathsig(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
