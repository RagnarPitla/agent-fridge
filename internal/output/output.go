// SPDX-License-Identifier: Apache-2.0
// Stream discipline: results on stdout, diagnostics on stderr.
// With --json, stdout is exactly one JSON object and nothing else.
// Output is plain ASCII with no ANSI escapes, so PowerShell and CI logs stay clean.
package output

import (
	"fmt"
	"io"
	"strings"

	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

// Ctx carries the global flags every command shares.
type Ctx struct {
	JSON    bool
	Quiet   bool
	Verbose bool
	NoColor bool
	Stdout  io.Writer
	Stderr  io.Writer
}

// Result is what a command hands to the emitter.
type Result struct {
	Data any
	Text string
}

// Emit prints one success result and returns exit code 0.
func Emit(ctx *Ctx, command string, res Result) int {
	if ctx.JSON {
		fmt.Fprint(ctx.Stdout, jsonx.Stable(jsonx.Obj{
			"ok": true, "protocol": brand.Protocol, "command": command,
			"exitCode": float64(0), "ts": util.Now(), "data": res.Data, "error": nil,
		}))
	} else if !ctx.Quiet && res.Text != "" {
		text := res.Text
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		fmt.Fprint(ctx.Stdout, text)
	}
	return 0
}

// EmitError prints one failure and returns its exit code.
func EmitError(ctx *Ctx, command string, err error) int {
	ae := errs.Coerce(err)
	code := ae.Code
	if code == "" {
		code = "E_INTERNAL"
	}
	exitCode := ae.ExitCode
	if exitCode == 0 {
		exitCode = 1
	}
	if ctx.JSON {
		var hint any
		if ae.Hint != "" {
			hint = ae.Hint
		}
		var details any
		if ae.Details != nil {
			details = ae.Details
		}
		fmt.Fprint(ctx.Stdout, jsonx.Stable(jsonx.Obj{
			"ok": false, "protocol": brand.Protocol, "command": command,
			"exitCode": float64(exitCode), "ts": util.Now(), "data": nil,
			"error": jsonx.Obj{"code": code, "message": ae.Msg, "hint": hint, "details": details},
		}))
	} else {
		if ae.Details != nil {
			if report := ae.Details.Str("report"); report != "" {
				fmt.Fprintf(ctx.Stderr, "%s\n", report)
			}
		}
		fmt.Fprintf(ctx.Stderr, "%s: %s\n", code, ae.Msg)
		if ae.Hint != "" {
			fmt.Fprintf(ctx.Stderr, "hint: %s\n", ae.Hint)
		}
	}
	return exitCode
}

// Warn writes one advisory line to stderr.
func Warn(ctx *Ctx, msg string) {
	if !ctx.Quiet {
		fmt.Fprintf(ctx.Stderr, "warning: %s\n", msg)
	}
}
