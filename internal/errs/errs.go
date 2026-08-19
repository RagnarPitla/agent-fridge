// SPDX-License-Identifier: Apache-2.0
// The exit-code contract. Stable across all 0.x releases.
// This table mirrors src/core/errors.mjs exactly; spec/exit-codes.md is
// generated from that file and TestExitTableMatchesSpec asserts they agree.
package errs

import (
	"errors"

	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
)

// Exit maps a symbolic code to its process exit status.
var Exit = map[string]int{
	"OK":                 0,
	"E_INTERNAL":         1,
	"E_USAGE":            2,
	"E_NOT_INITIALIZED":  3,
	"E_PROTOCOL_VERSION": 4,
	"E_STATE_CORRUPT":    5,
	"E_PERMISSION":       6,
	"E_NO_SESSION":       7,
	"E_CONFLICT":         10,
	"E_NOT_FOUND":        11,
	"E_NOT_OWNER":        12,
	"E_LEASE_EXPIRED":    13,
	"E_OUT_OF_SCOPE":     14,
	"E_ALREADY_EXISTS":   15,
	"E_MUTEX_TIMEOUT":    20,
	"E_WAIT_TIMEOUT":     21,
	"E_QUEUE_ABANDONED":  22,
	"E_DRIFT":            30,
	"E_NONCONFORMANT":    31,
	"E_PATH_INVALID":     40,
	"E_FOREIGN_HOST":     41,
}

// ExitDoc is the one-line meaning of every code, used by --help and by the
// generated spec/exit-codes.md.
var ExitDoc = map[string]string{
	"OK":                 "Success.",
	"E_INTERNAL":         "Unexpected internal error (a bug). Re-run with --verbose for a stack trace.",
	"E_USAGE":            "Bad arguments: unknown flag, missing required flag, invalid duration.",
	"E_NOT_INITIALIZED":  "No .fridge/ found from the current directory upward. Run: fridge init",
	"E_PROTOCOL_VERSION": ".fridge/VERSION is a protocol version this binary does not support.",
	"E_STATE_CORRUPT":    "A record is unparseable or invalid, or a write could not be completed.",
	"E_PERMISSION":       "Permission denied or read-only filesystem under .fridge/.",
	"E_NO_SESSION":       "No actor/session could be resolved. Run: fridge join --agent <name>",
	"E_CONFLICT":         "The requested scope overlaps a claim held by someone else.",
	"E_NOT_FOUND":        "No such claim, message, actor, or queue entry.",
	"E_NOT_OWNER":        "You do not hold the token for that claim.",
	"E_LEASE_EXPIRED":    "Your claim already expired and was reaped.",
	"E_OUT_OF_SCOPE":     "That path is not covered by any claim you hold.",
	"E_ALREADY_EXISTS":   "Already exists (workspace, actor, or record).",
	"E_MUTEX_TIMEOUT":    "Could not acquire the registry mutex before the deadline.",
	"E_WAIT_TIMEOUT":     "Wait deadline reached.",
	"E_QUEUE_ABANDONED":  "The queue entry expired or was cancelled.",
	"E_DRIFT":            "A --check found a problem: doctor findings, unrendered door, or stale adapter block.",
	"E_NONCONFORMANT":    "This build disagrees with the protocol vectors. Run: fridge conform --verbose",
	"E_PATH_INVALID":     "Path rejected: traversal, escape, reserved location, or unsupported glob.",
	"E_FOREIGN_HOST":     "That claim belongs to another host. Pass --allow-multihost to override.",
}

// AppError is a deliberate, documented refusal. Every non-zero exit carries one.
type AppError struct {
	Code     string
	Msg      string
	Hint     string
	Details  jsonx.Obj
	ExitCode int
}

func (e *AppError) Error() string { return e.Msg }

// New builds an AppError. An unknown code is a programming mistake and panics,
// mirroring the TypeError the Node constructor throws.
func New(code, msg string) *AppError {
	exit, ok := Exit[code]
	if !ok {
		panic("AppError: unknown exit code '" + code + "'. Add it to Exit and ExitDoc first.")
	}
	return &AppError{Code: code, Msg: msg, ExitCode: exit}
}

// WithHint returns the same error carrying a next step for the human.
func (e *AppError) WithHint(hint string) *AppError { e.Hint = hint; return e }

// WithDetails attaches structured context that --json surfaces verbatim.
func (e *AppError) WithDetails(d jsonx.Obj) *AppError { e.Details = d; return e }

// Internal wraps any non-AppError as E_INTERNAL, which is exit 1.
func Internal(err error) *AppError {
	return New("E_INTERNAL", err.Error())
}

// As returns err as an *AppError, wrapping anything unexpected as E_INTERNAL.
func Coerce(err error) *AppError {
	if err == nil {
		return nil
	}
	if ae, ok := err.(*AppError); ok {
		return ae
	}
	return Internal(err)
}

// As is the non-normalising lookup: it returns nil for errors this package did
// not create, so callers can test for a specific code without inventing one.
func As(err error) *AppError {
	var ae *AppError
	if errors.As(err, &ae) {
		return ae
	}
	return nil
}
