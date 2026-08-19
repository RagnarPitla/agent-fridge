// SPDX-License-Identifier: Apache-2.0
package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/mutex"
	"github.com/RagnarPitla/agent-fridge/internal/output"
	"github.com/RagnarPitla/agent-fridge/internal/render"
	"github.com/RagnarPitla/agent-fridge/internal/store"
)

// Flags holds parsed flag values: bool, string, or []string.
type Flags map[string]any

// Bool reads a boolean flag.
func (f Flags) Bool(name string) bool {
	v, ok := f[name]
	if !ok {
		return false
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return true
}

// Str reads a string flag, or "" when it was never given.
func (f Flags) Str(name string) string {
	v, ok := f[name]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case []string:
		if len(t) > 0 {
			return t[0]
		}
	}
	return ""
}

// Has reports whether the flag was given at all.
func (f Flags) Has(name string) bool { _, ok := f[name]; return ok }

// List reads a repeatable flag as a slice.
func (f Flags) List(name string) []string {
	v, ok := f[name]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case []string:
		return t
	case string:
		return []string{t}
	}
	return nil
}

// Ctx is one invocation: parsed arguments plus the output streams.
type Ctx struct {
	Command    string
	Positional []string
	Flags      Flags
	Rest       []string
	JSON       bool
	Quiet      bool
	Verbose    bool
	Cwd        string
	Out        *output.Ctx
}

func (c *Ctx) emit(command string, res output.Result) (int, error) {
	return output.Emit(c.Out, command, res), nil
}

func (c *Ctx) warn(msg string) { output.Warn(c.Out, msg) }

// Spec describes one command and its flags.
type Spec struct {
	Name    string
	Fn      func(*Ctx) (int, error)
	Summary string
	Bool    []string
	Value   []string
	Exits   []string
}

var globalBool = []string{"json", "quiet", "verbose", "no-color", "yes", "help", "allow-multihost", "allow-secret-like"}
var globalValue = []string{"repo", "agent", "vendor"}

// Order is the command table order, which usage() prints verbatim.
var Order []string

var specs map[string]*Spec

// Aliases are the friendly second names for five commands.
var Aliases = map[string]string{"note": "pin", "tidy": "doctor", "sweep": "reap", "pass": "handoff", "door": "board"}

var aliasOrder = []string{"note", "tidy", "sweep", "pass", "door"}

func init() {
	table := []*Spec{
		{Name: "init", Fn: cmdInit, Summary: "Hang the door: create .fridge/ in this repository.", Bool: []string{"force", "no-adapters"}, Value: []string{"commit-notes"}, Exits: []string{"E_ALREADY_EXISTS", "E_PERMISSION"}},
		{Name: "join", Fn: cmdJoin, Summary: "Put your name on the door and start a session.", Bool: []string{}, Value: []string{}, Exits: []string{"E_NOT_INITIALIZED", "E_USAGE"}},
		{Name: "whoami", Fn: cmdWhoami, Summary: "Who am I, and what am I holding?", Bool: []string{}, Value: []string{}, Exits: []string{"E_NO_SESSION"}},
		{Name: "claim", Fn: cmdClaim, Summary: "Take a chore card over one or more paths.", Bool: []string{"queue", "strict", "confirm-global"}, Value: []string{"task", "mode", "ttl", "exclude", "wait", "label"}, Exits: []string{"E_CONFLICT", "E_PATH_INVALID", "E_MUTEX_TIMEOUT", "E_WAIT_TIMEOUT"}},
		{Name: "check", Fn: cmdCheck, Summary: "May I write these paths right now?", Bool: []string{"for-write"}, Value: []string{}, Exits: []string{"E_CONFLICT", "E_OUT_OF_SCOPE", "E_PATH_INVALID"}},
		{Name: "guard", Fn: cmdGuard, Summary: "Assert paths are inside your claims (for hooks and pre-commit).", Bool: []string{"staged"}, Value: []string{}, Exits: []string{"E_CONFLICT", "E_OUT_OF_SCOPE"}},
		{Name: "heartbeat", Fn: cmdHeartbeat, Summary: "Shout \"still on it\" and renew your leases.", Bool: []string{"all"}, Value: []string{"claim", "ttl"}, Exits: []string{"E_LEASE_EXPIRED", "E_NOT_FOUND"}},
		{Name: "extend", Fn: cmdExtend, Summary: "Raise the TTL on one claim.", Bool: []string{}, Value: []string{"ttl"}, Exits: []string{"E_NOT_FOUND", "E_NOT_OWNER"}},
		{Name: "release", Fn: cmdRelease, Summary: "Take the card down.", Bool: []string{"all", "force"}, Value: []string{"outcome", "note"}, Exits: []string{"E_NOT_FOUND", "E_NOT_OWNER", "E_LEASE_EXPIRED"}},
		{Name: "reap", Fn: cmdReap, Summary: "Sweep cards that fell off the door.", Bool: []string{"dry-run", "force"}, Value: []string{}, Exits: []string{"E_MUTEX_TIMEOUT"}},
		{Name: "wait", Fn: cmdWait, Summary: "Wait for a card to come down.", Bool: []string{}, Value: []string{"timeout"}, Exits: []string{"E_WAIT_TIMEOUT", "E_NOT_FOUND"}},
		{Name: "run", Fn: cmdRun, Summary: "Claim, run a command with automatic check-ins, then release.", Bool: []string{"keep-on-failure"}, Value: []string{"claim", "task", "ttl", "mode"}, Exits: []string{"E_CONFLICT", "E_USAGE"}},
		{Name: "pin", Fn: cmdPin, Summary: "Pin a durable note to the door.", Value: []string{"kind", "claim", "task"}, Exits: []string{"E_USAGE"}},
		{Name: "log", Fn: cmdLog, Summary: "Read the notes wall.", Bool: []string{"follow"}, Value: []string{"limit", "since", "until", "actor", "type"}, Exits: []string{}},
		{Name: "board", Fn: cmdBoard, Summary: "Read the door.", Bool: []string{"write", "stdout", "check", "wide"}, Value: []string{}, Exits: []string{"E_DRIFT"}},
		{Name: "status", Fn: cmdStatus, Summary: "Same data as the door, machine first.", Bool: []string{"mine", "wide", "watch"}, Value: []string{"interval"}, Exits: []string{}},
		{Name: "render", Fn: cmdRender, Summary: "Regenerate the door and views.", Bool: []string{"check"}, Value: []string{"output"}, Exits: []string{"E_DRIFT"}},
		{Name: "handoff", Fn: cmdHandoff, Summary: "Offer a chore to another housemate.", Bool: []string{"force"}, Value: []string{"to", "note", "reason"}, Exits: []string{"E_NOT_FOUND", "E_NOT_OWNER", "E_USAGE"}},
		{Name: "accept", Fn: cmdAccept, Summary: "Take an offered chore.", Bool: []string{}, Value: []string{}, Exits: []string{"E_NOT_FOUND"}},
		{Name: "decline", Fn: cmdDecline, Summary: "Refuse an offered chore.", Bool: []string{}, Value: []string{"reason"}, Exits: []string{"E_NOT_FOUND"}},
		{Name: "inbox", Fn: cmdInbox, Summary: "Notes addressed to me.", Bool: []string{}, Value: []string{}, Exits: []string{}},
		{Name: "doctor", Fn: cmdDoctor, Summary: "Tidy the door: diagnose and repair.", Bool: []string{"fix", "check"}, Value: []string{}, Exits: []string{"E_DRIFT"}},
		{Name: "simulate", Fn: cmdSimulate, Summary: "Run a real multi-process household simulation.", Bool: []string{}, Value: []string{"agents", "duration", "seed", "report"}, Exits: []string{}},
		{Name: "conform", Fn: cmdConform, Summary: "Check this build against the protocol vectors.", Bool: []string{}, Value: []string{"vectors", "suite"}, Exits: []string{"E_NONCONFORMANT", "E_NOT_FOUND"}},
		{Name: "adapters", Fn: cmdAdapters, Summary: "Install or check vendor instruction blocks.", Bool: []string{"check", "print"}, Value: []string{"vendor"}, Exits: []string{"E_DRIFT", "E_USAGE"}},
		{Name: "migrate", Fn: cmdMigrate, Summary: "Import legacy shared Markdown files into the notes wall.", Bool: []string{"dry-run", "freeze"}, Value: []string{"todo-done", "updates", "author-map"}, Exits: []string{"E_USAGE", "E_NOT_FOUND"}},
		{Name: "config", Fn: cmdConfig, Summary: "Read or write .fridge/config.json.", Bool: []string{}, Value: []string{}, Exits: []string{"E_USAGE", "E_NOT_FOUND"}},
		{Name: "version", Fn: cmdVersion, Summary: "Version and protocol information.", Bool: []string{}, Value: []string{}, Exits: []string{}},
	}
	specs = map[string]*Spec{}
	Order = nil
	for _, s := range table {
		specs[s.Name] = s
		Order = append(Order, s.Name)
	}
}

// Parsed is the result of argument parsing.
type Parsed struct {
	Command    string
	Positional []string
	Flags      Flags
	Rest       []string
}

// ParseArgs splits argv into command, positionals, flags and the post-`--` rest.
func ParseArgs(argv []string) (Parsed, error) {
	out := Parsed{Positional: []string{}, Flags: Flags{}, Rest: []string{}}
	args := append([]string{}, argv...)
	for i, a := range args {
		if a == "--" {
			out.Rest = append([]string{}, args[i+1:]...)
			args = args[:i]
			break
		}
	}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		out.Command = args[0]
		args = args[1:]
	}
	var spec *Spec
	if out.Command != "" {
		name := out.Command
		if a, ok := Aliases[name]; ok {
			name = a
		}
		spec = specs[name]
	}
	boolFlags := map[string]bool{}
	for _, f := range globalBool {
		boolFlags[f] = true
	}
	valueFlags := map[string]bool{}
	for _, f := range globalValue {
		valueFlags[f] = true
	}
	if spec != nil {
		for _, f := range spec.Bool {
			boolFlags[f] = true
		}
		for _, f := range spec.Value {
			valueFlags[f] = true
		}
	}
	for i := 0; i < len(args); i++ {
		token := args[i]
		if token == "-h" || token == "--help" {
			out.Flags["help"] = true
			continue
		}
		if token == "-v" || token == "--version" {
			out.Flags["version"] = true
			continue
		}
		if !strings.HasPrefix(token, "--") {
			out.Positional = append(out.Positional, token)
			continue
		}
		eq := strings.Index(token, "=")
		var name, inline string
		hasInline := false
		if eq == -1 {
			name = strings.TrimSpace(token[2:])
		} else {
			name = strings.TrimSpace(token[2:eq])
			inline = token[eq+1:]
			hasInline = true
		}
		if boolFlags[name] {
			if hasInline {
				out.Flags[name] = inline != "false" && inline != "0"
			} else {
				out.Flags[name] = true
			}
			continue
		}
		if valueFlags[name] {
			value := inline
			if !hasInline {
				i++
				if i >= len(args) {
					return out, errs.New("E_USAGE", fmt.Sprintf("Flag --%s needs a value.", name))
				}
				value = args[i]
			}
			if name == "exclude" || name == "label" || name == "claim" {
				out.Flags[name] = append(out.Flags.List(name), value)
			} else {
				out.Flags[name] = value
			}
			continue
		}
		cmdName := out.Command
		if cmdName == "" {
			cmdName = "fridge"
		}
		hint := strings.TrimSpace(brand.Bin + " " + out.Command + " --help")
		return out, errs.New("E_USAGE", fmt.Sprintf("Unknown flag --%s for '%s'.", name, cmdName)).WithHint(hint)
	}
	if out.Command != "" {
		if a, ok := Aliases[out.Command]; ok {
			out.Command = a
		}
	}
	return out, nil
}

func usage() string {
	rows := []string{}
	for _, name := range Order {
		rows = append(rows, "  "+padEnd(name, 11)+specs[name].Summary)
	}
	pairs := []string{}
	for _, a := range aliasOrder {
		pairs = append(pairs, a+"="+Aliases[a])
	}
	lines := []string{
		fmt.Sprintf("%s %s (protocol %s)", brand.Product, brand.Version, brand.Protocol),
		"",
		brand.Tagline + " Everyone pins their own note. Nobody erases the board.",
		"",
		fmt.Sprintf("usage: %s <command> [args] [flags]", brand.Bin),
		"",
		"commands:",
	}
	lines = append(lines, rows...)
	lines = append(lines,
		"",
		"aliases: "+strings.Join(pairs, ", "),
		"",
		"global flags: --json --quiet --verbose --no-color --repo <path> --agent <name> --allow-secret-like --help",
		"",
		fmt.Sprintf("60-second start:  %s init && %s join --agent me && %s claim \"src/**\" --task \"work\" && %s board", brand.Bin, brand.Bin, brand.Bin, brand.Bin),
		"docs: https://github.com/RagnarPitla/"+brand.Package,
	)
	return strings.Join(lines, "\n")
}

func commandHelp(name string) string {
	c := specs[name]
	flags := []string{}
	for _, f := range c.Bool {
		flags = append(flags, "--"+f)
	}
	for _, f := range c.Value {
		flags = append(flags, "--"+f+" <value>")
	}
	exits := []string{}
	for _, code := range append([]string{"OK"}, c.Exits...) {
		exits = append(exits, fmt.Sprintf("  %s  %s %s", padStart(fmt.Sprintf("%d", errs.Exit[code]), 3), padEnd(code, 20), errs.ExitDoc[code]))
	}
	flagText := "(none beyond global flags)"
	if len(flags) > 0 {
		flagText = strings.Join(flags, " ")
	}
	lines := []string{
		fmt.Sprintf("%s %s - %s", brand.Bin, name, c.Summary),
		"",
		"flags: " + flagText,
		"",
		"exit codes:",
	}
	lines = append(lines, exits...)
	return strings.Join(lines, "\n")
}

func padEnd(s string, n int) string {
	if len(s) < n {
		return s + strings.Repeat(" ", n-len(s))
	}
	return s
}

func padStart(s string, n int) string {
	if len(s) < n {
		return strings.Repeat(" ", n-len(s)) + s
	}
	return s
}

var allowedEnv = map[string]bool{
	"FRIDGE_REPO": true, "FRIDGE_ACTOR": true, "FRIDGE_SESSION": true, "FRIDGE_CLAIM_TOKEN": true,
	"FRIDGE_TTL": true, "FRIDGE_JSON": true, "FRIDGE_NO_RENEW": true, "FRIDGE_TEST": true, "FRIDGE_FAULT": true,
}

// Main runs one invocation and returns the process exit code.
func Main(argv []string, stdout, stderr writer) int {
	cwd, _ := os.Getwd()
	outCtx := &output.Ctx{Stdout: stdout, Stderr: stderr}
	ctx := &Ctx{Command: "fridge", Cwd: cwd, Flags: Flags{}, Out: outCtx}
	code, err := run(ctx, argv)
	if err != nil {
		name := ctx.Command
		if name == "" {
			name = "fridge"
		}
		return output.EmitError(ctx.Out, name, err)
	}
	return code
}

type writer = interface{ Write([]byte) (int, error) }

func renewForInvocation(ctx *Ctx) error {
	explicit := ctx.Flags.Str("agent")
	if explicit == "" {
		explicit = os.Getenv("FRIDGE_ACTOR")
	}
	if explicit == "" || ctx.Command == "init" || ctx.Command == "join" || ctx.Command == "version" || ctx.Command == "conform" {
		return nil
	}
	ws, err := open(ctx)
	if err != nil {
		return nil // the command reports workspace errors through its normal path
	}
	_, session, err := store.RequireActor(ws, explicit, "")
	if err != nil {
		if ctx.Flags.Str("agent") != "" {
			return err
		}
		if app := errs.As(err); app != nil && app.Code == "E_NO_SESSION" {
			return nil // stale environment identity
		}
		return err
	}
	if os.Getenv("FRIDGE_NO_RENEW") == "1" {
		return nil
	}
	_ = mutex.With(ws, "renew", func() error {
		renewed, err := store.RenewOwnLeases(ws, session)
		if err == nil && len(renewed) > 0 {
			render.Auto(ws)
		}
		return err
	}, nil)
	return nil
}

func run(ctx *Ctx, argv []string) (int, error) {
	parsed, err := ParseArgs(argv)
	if err != nil {
		return 0, err
	}
	ctx.Command = parsed.Command
	ctx.Positional = parsed.Positional
	ctx.Flags = parsed.Flags
	ctx.Rest = parsed.Rest
	ctx.JSON = parsed.Flags.Bool("json") || os.Getenv("FRIDGE_JSON") == "1"
	ctx.Quiet = parsed.Flags.Bool("quiet")
	ctx.Verbose = parsed.Flags.Bool("verbose")
	ctx.Out.JSON = ctx.JSON
	ctx.Out.Quiet = ctx.Quiet
	ctx.Out.Verbose = ctx.Verbose
	ctx.Out.NoColor = parsed.Flags.Bool("no-color")

	if parsed.Flags.Bool("version") && parsed.Command == "" {
		fmt.Fprintf(ctx.Out.Stdout, "%s %s (%s)\n", brand.Package, brand.Version, brand.Protocol)
		return 0, nil
	}
	if parsed.Command == "" {
		fmt.Fprintf(ctx.Out.Stdout, "%s\n", usage())
		return 0, nil
	}
	if parsed.Command == "help" {
		target := ""
		if len(parsed.Positional) > 0 {
			target = parsed.Positional[0]
		}
		if target != "" && specs[target] != nil {
			fmt.Fprintf(ctx.Out.Stdout, "%s\n", commandHelp(target))
		} else {
			fmt.Fprintf(ctx.Out.Stdout, "%s\n", usage())
		}
		return 0, nil
	}
	if parsed.Command == simWorkerCommand {
		return simWorker(ctx.Out.Stdout), nil
	}
	spec := specs[parsed.Command]
	if spec == nil {
		return 0, errs.New("E_USAGE", fmt.Sprintf("Unknown command '%s'.", parsed.Command)).WithHint(brand.Bin + " help")
	}
	if parsed.Flags.Bool("help") {
		fmt.Fprintf(ctx.Out.Stdout, "%s\n", commandHelp(parsed.Command))
		return 0, nil
	}
	for _, kv := range os.Environ() {
		k := kv
		if i := strings.Index(kv, "="); i != -1 {
			k = kv[:i]
		}
		if strings.HasPrefix(k, "FRIDGE_") && !allowedEnv[k] {
			fmt.Fprintf(ctx.Out.Stderr, "warning: unknown environment variable %s (typo?)\n", k)
		}
	}
	if os.Getenv("FRIDGE_FAULT") != "" && os.Getenv("FRIDGE_TEST") != "1" {
		return 0, errs.New("E_USAGE", "FRIDGE_FAULT is only honoured when FRIDGE_TEST=1.")
	}
	if err := renewForInvocation(ctx); err != nil {
		return 0, err
	}
	return spec.Fn(ctx)
}
