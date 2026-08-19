// SPDX-License-Identifier: Apache-2.0
// Notes are write-once files. Two writers can never collide, because they never
// touch the same file. This is the whole answer to the 128-lines-overwritten bug.
package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/output"
	"github.com/RagnarPitla/agent-fridge/internal/render"
	"github.com/RagnarPitla/agent-fridge/internal/secrets"
	"github.com/RagnarPitla/agent-fridge/internal/store"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

// LooksSecret names the first credential shape it recognises, or "".
// Kept here so existing callers do not have to change; the table lives in
// internal/secrets so every durable field is checked against the same list.
func LooksSecret(text string) string { return secrets.Looks(text) }

func readStdin(in io.Reader) string {
	if f, ok := in.(*os.File); ok {
		st, err := f.Stat()
		if err != nil || st.Mode()&os.ModeCharDevice != 0 {
			return ""
		}
	}
	data, err := io.ReadAll(in)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func cmdPin(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	actor, session, err := store.RequireActorMutating(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	text := strings.TrimSpace(strings.Join(ctx.Positional, " "))
	if text == "" {
		text = readStdin(os.Stdin)
	}
	if text == "" {
		return 0, errs.New("E_USAGE", "A note needs some words.").
			WithHint(brand.Bin + " pin \"rewrote the retry loop in src/api\"")
	}
	kind := ctx.Flags.Str("kind")
	if kind == "" {
		kind = "note"
	}
	if err := secrets.Guard(map[string]string{
		"That note": text, "--task": ctx.Flags.Str("task"), "--kind": kind,
	}, ctx.Flags.Bool("allow-secret-like")); err != nil {
		return 0, err
	}
	var subject any
	if c := ctx.Flags.List("claim"); len(c) > 0 {
		subject = jsonx.Obj{"kind": "claim", "id": c[0]}
	}
	var taskVal any
	if t := ctx.Flags.Str("task"); t != "" {
		taskVal = t
	}
	note, err := store.Pin(ws, store.PinArgs{
		Type: "note." + kind, Actor: actor, Session: session, Subject: subject,
		Summary: sliceStr(strings.SplitN(text, "\n", 2)[0], 300),
		Data:    jsonx.Obj{"body": text, "kind": kind, "task": taskVal},
	})
	if err != nil {
		return 0, err
	}
	render.Auto(ws)
	return ctx.emit("pin", output.Result{
		Data: jsonx.Obj{"noteId": note.Str("id"), "ts": note.Str("ts"), "type": note.Str("type"),
			"summary": note.Str("summary")},
		Text: "pinned " + note.Str("id"),
	})
}

func fmtNote(n jsonx.Obj) string {
	return fmt.Sprintf("%s  %s  %s  %s", n.Str("ts"),
		cutPad(n.Str("actorName"), 14), cutPad(n.Str("type"), 18), n.Str("summary"))
}

func cutPad(s string, n int) string {
	padded := padEnd(s, n)
	if len(padded) > n {
		return padded[:n]
	}
	return padded
}

func cmdLog(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	limit := 50
	if l := ctx.Flags.Str("limit"); l != "" {
		n, err := strconv.ParseFloat(l, 64)
		if err != nil || n < 1 {
			return 0, errs.New("E_USAGE", "--limit must be a positive number.")
		}
		limit = int(n)
	}
	filter := store.NoteFilter{Limit: limit, Actor: ctx.Flags.Str("actor"), Type: ctx.Flags.Str("type")}
	if s := ctx.Flags.Str("since"); s != "" {
		d, err := util.ParseDuration(s, "since")
		if err != nil {
			return 0, err
		}
		filter.Since = util.NowMs() - d
	}
	if u := ctx.Flags.Str("until"); u != "" {
		d, err := util.ParseDuration(u, "until")
		if err != nil {
			return 0, err
		}
		filter.Until = util.NowMs() - d
	}
	if !ctx.Flags.Bool("follow") {
		notes := store.ReadNotes(ws, filter)
		arr := jsonx.Arr{}
		lines := []string{}
		for _, n := range notes {
			arr = append(arr, n)
			lines = append(lines, fmtNote(n))
		}
		text := strings.Join(lines, "\n")
		if text == "" {
			text = "no notes yet"
		}
		return ctx.emit("log", output.Result{Data: jsonx.Obj{"notes": arr}, Text: text})
	}
	seen := map[string]bool{}
	seedFilter := filter
	seedFilter.Limit = 1000
	for _, n := range store.ReadNotes(ws, seedFilter) {
		seen[n.Str("id")] = true
	}
	w := bufio.NewWriter(ctx.Out.Stdout)
	for _, n := range store.ReadNotes(ws, filter) {
		fmt.Fprintf(w, "%s\n", fmtNote(n))
	}
	w.Flush()
	tail := filter
	tail.Limit = 200
	for {
		util.Sleep(750)
		for _, n := range store.ReadNotes(ws, tail) {
			if seen[n.Str("id")] {
				continue
			}
			seen[n.Str("id")] = true
			fmt.Fprintf(w, "%s\n", fmtNote(n))
		}
		w.Flush()
	}
}
