// SPDX-License-Identifier: Apache-2.0
//go:build windows

package util

import "os"

// ProcessAlive answers "is that pid still there". Windows has no signal 0, so
// this asks the OS for a handle instead; FindProcess fails for a pid that is
// gone, which is the same answer process.kill(pid, 0) gives under Node.
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
