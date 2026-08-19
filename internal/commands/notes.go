// SPDX-License-Identifier: Apache-2.0
// Notes are write-once files. Two writers can never collide, because they never
// touch the same file. This is the whole answer to the 128-lines-overwritten bug.
package commands

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/output"
	"github.com/RagnarPitla/agent-fridge/internal/render"
	"github.com/RagnarPitla/agent-fridge/internal/store"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

type secretPattern struct {
	re   *regexp.Regexp
	what string
}

var secrety = []secretPattern{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), "a private key"},
	{regexp.MustCompile(`\bghp_[A-Za-z0-9]{20,}\b`), "a GitHub token"},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`), "a GitHub fine-grained token"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "an AWS access key id"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`), "an OpenAI-style key"},
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`), "a Slack token"},
	{regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|client[_-]?secret)\s*[=:]\s*\S{8,}`), "a credential assignment"},
}

// LooksSecret names the first credential shape it recognises, or "".
func LooksSecret(text string) string {
	for _, p := range secrety {
		if p.re.MatchString(text) {
			return p.what
		}
	}
	return ""
}

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
	actor, session, err := store.RequireActor(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	if _, err := maybeRenew(ws, session); err != nil {
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
	if found := LooksSecret(text); found != "" && !ctx.Flags.Bool("allow-secret-like") {
		return 0, errs.New("E_USAGE", fmt.Sprintf("That note looks like it contains %s.", found)).
			WithHint("Notes are committed history. Remove it, or pass --allow-secret-like if it is a false positive.")
	}
	kind := ctx.Flags.Str("kind")
	if kind == "" {
		kind = "note"
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

// maybeRenew is piggyback renewal: any command you run is proof you are still
// alive, so it refreshes your own leases when they are more than half used up.
// No daemon needed.
func maybeRenew(ws *store.Workspace, session jsonx.Obj) ([]string, error) {
	if session == nil || os.Getenv("FRIDGE_NO_RENEW") == "1" {
		return nil, nil
	}
	if !ws.Config.Bool("lease.renewOnAnyCommand") {
		return nil, nil
	}
	ratio := ws.Config.Num("lease.renewThresholdRatio")
	renewed := []string{}
	for _, d := range store.ListClaims(ws, true) {
		c := d.Claim
		if c.Str("sessionId") != session.Str("id") || d.Stale {
			continue
		}
		ttl := int64(c.Num("ttlMs"))
		if ttl == 0 {
			ttl = int64(ws.Config.Num("lease.defaultTtlMs"))
		}
		if float64(d.ExpiresInMs) > float64(ttl)*ratio {
			continue
		}
		if _, err := store.WriteLease(ws, c.Str("id"), session.Str("id"), ttl, int(d.Lease.Num("renewals"))+1); err != nil {
			return nil, err
		}
		renewed = append(renewed, c.Str("id"))
	}
	return renewed, nil
}
