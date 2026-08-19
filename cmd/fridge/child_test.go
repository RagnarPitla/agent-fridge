// SPDX-License-Identifier: Apache-2.0
package main

import (
	"os"
	"os/exec"
	"strings"
)

// runChild runs one child process to completion and returns its output and exit
// status. A process killed by a signal reports a non-zero status here, which is
// all the crash tests need to know.
func runChild(bin string, args []string, dir string, env []string) (stdout, stderr string, code int) {
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
			if code < 0 {
				code = 137
			}
		} else {
			code = -1
			errBuf.WriteString(err.Error())
		}
	}
	return out.String(), errBuf.String(), code
}

// hardKillSelf ends this process the way a closed terminal or an OOM kill does:
// no defers, no cleanup, no goodbye.
func hardKillSelf() {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		os.Exit(137)
	}
	_ = p.Signal(os.Kill)
	select {}
}
