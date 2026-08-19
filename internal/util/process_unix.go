// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package util

import "syscall"

// ProcessAlive answers "is that pid still there", the same question
// process.kill(pid, 0) answers in the Node implementation. EPERM means the
// process exists but belongs to somebody else, which still counts as alive.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return err == syscall.EPERM
}
