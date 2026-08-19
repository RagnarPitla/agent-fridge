// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package commands

import (
	"os/exec"
	"syscall"
)

// signalNumber maps a terminating signal onto the number Node reports, so that
// `fridge run` produces the same 128+n exit code in both implementations.
func signalNumber(ee *exec.ExitError) int {
	ws, ok := ee.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() {
		return 0
	}
	switch ws.Signal() {
	case syscall.SIGINT:
		return 2
	case syscall.SIGTERM:
		return 15
	case syscall.SIGKILL:
		return 9
	}
	return 0
}
