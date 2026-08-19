// SPDX-License-Identifier: Apache-2.0

// Package vectors holds the language-neutral conformance vectors for the
// protocol, and embeds them into the binary.
//
// This directory is the single canonical home for the vectors. The Go binary
// embeds it, the Go tests read it, the Node implementation reads it, and any
// third-party implementation can copy it. There is deliberately no second
// copy: a project whose entire premise is "one writer per record" should not
// keep two copies of its own contract.
package vectors

import "embed"

// FS is every vector file, embedded so a downloaded release binary can prove
// itself with no checkout, no network, and no second file to install.
//
//go:embed *.json
var FS embed.FS

// Dir is the subdirectory inside FS. Empty because the files sit at the root
// of this package.
const Dir = "."
