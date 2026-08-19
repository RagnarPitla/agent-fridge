// SPDX-License-Identifier: Apache-2.0
//go:build windows

package commands

import "os/exec"

// signalNumber has no meaning on Windows, where processes are not signalled.
func signalNumber(_ *exec.ExitError) int { return 0 }
