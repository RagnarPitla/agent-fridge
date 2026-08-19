// SPDX-License-Identifier: Apache-2.0
// Agent Fridge - stop AI coding agents from overwriting each other's work.
package main

import (
	"os"

	"github.com/RagnarPitla/agent-fridge/internal/commands"
)

func main() {
	os.Exit(commands.Main(os.Args[1:], os.Stdout, os.Stderr))
}
