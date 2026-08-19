// SPDX-License-Identifier: Apache-2.0
// Agent Fridge Board - one shared fridge door for every coding agent in your checkout.
package main

import (
	"os"

	"github.com/RagnarPitla/agent-fridge/internal/commands"
)

func main() {
	os.Exit(commands.Main(os.Args[1:], os.Stdout, os.Stderr))
}
