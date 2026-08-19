// SPDX-License-Identifier: Apache-2.0
// Rename surface: product naming lives here and in package.json only.
// These constants deliberately match src/brand.mjs byte for byte, because the
// Go binary and the Node CLI write the same records into the same .fridge/.
package brand

const (
	Product  = "Agent Fridge Board"
	Package  = "agent-fridge"
	Bin      = "fridge"
	Version  = "0.2.1"
	Protocol = "wcp/0.1"
	StateDir = ".fridge"
	Writer   = Package + "/" + Version
)
