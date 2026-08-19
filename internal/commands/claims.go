// SPDX-License-Identifier: Apache-2.0
// Chore cards: claim, check, heartbeat, extend, release, reap, wait, guard, run.
package commands

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/mutex"
	"github.com/RagnarPitla/agent-fridge/internal/output"
	"github.com/RagnarPitla/agent-fridge/internal/paths"
	"github.com/RagnarPitla/agent-fridge/internal/render"
	"github.com/RagnarPitla/agent-fridge/internal/secrets"
	"github.com/RagnarPitla/agent-fridge/internal/store"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

func currentBranch(root string) any {
	cmd := exec.Command("git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return strings.TrimSpace(string(out))
}

func normalizeAll(ws *store.Workspace, inputs []string, confirmGlobal bool) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, raw := range inputs {
		n, err := paths.NormalizePattern(raw, ws.Root, ws.Cwd)
		if err != nil {
			return nil, err
		}
		if paths.IsGlobal(n.Pattern) && !confirmGlobal && !ws.Config.Bool("paths.allowGlobalClaims") {
			return nil, errs.New("E_USAGE", fmt.Sprintf("'%s' would claim the whole repository.", raw)).
				WithHint("Claim the narrowest paths you need, or pass --confirm-global if you really mean it.")
		}
		p := n.Pattern
		if n.DirIntent && !n.IsGlob {
			p = p + "/**"
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out, nil
}

func buildScope(ws *store.Workspace, include, exclude []string) (jsonx.Obj, error) {
	insensitive := paths.DefaultCaseInsensitive(ws.Config.Str("paths.caseSensitivity"))
	limit := ws.Config.Int("paths.materializeLimit")
	m, err := paths.Materialize(ws.Root, include, paths.MaterializeOptions{Limit: limit, Insensitive: insensitive})
	if err != nil {
		return nil, err
	}
	drop := map[string]bool{}
	if len(exclude) > 0 {
		ex, err := paths.Materialize(ws.Root, exclude, paths.MaterializeOptions{
			Limit: limit, Insensitive: insensitive, Files: m.Materialized,
		})
		if err != nil {
			return nil, err
		}
		for _, f := range ex.Materialized {
			drop[f] = true
		}
	}
	kept := []string{}
	for _, f := range m.Materialized {
		if !drop[f] {
			kept = append(kept, f)
		}
	}
	return jsonx.Obj{
		"include":               strArr(include),
		"exclude":               strArr(exclude),
		"materialized":          strArr(kept),
		"materializedTruncated": m.Truncated,
		"matchers":              strArr(m.Matchers),
		"materializer":          m.Materializer,
	}, nil
}

func scopeFrom(o jsonx.Obj) paths.Scope {
	return paths.Scope{
		Include:      o.Strings("include"),
		Exclude:      o.Strings("exclude"),
		Materialized: o.Strings("materialized"),
		Truncated:    o.Bool("materializedTruncated"),
		Matchers:     o.Strings("matchers"),
	}
}

// ModesCollide: exclusive blocks everything except advisory, and shared
// coexists with shared.
func ModesCollide(requested, existing string) bool {
	if requested == "advisory" || existing == "advisory" {
		return false
	}
	if requested == "shared" && existing == "shared" {
		return false
	}
	return true
}

type conflict struct {
	Holder  store.Decorated
	Overlap paths.Overlap
}

func conflictReport(conflicts []conflict) string {
	lines := []string{"Somebody already has that chore.", ""}
	for _, c := range conflicts {
		d := c.Holder
		pid := "?"
		if p, ok := d.Claim.Get("process.pid").(float64); ok {
			pid = jsonx.FormatNumber(p)
		}
		task := d.Claim.Str("task")
		if task == "" {
			task = "-"
		}
		hit := c.Overlap.Paths
		if len(hit) > 5 {
			hit = hit[:5]
		}
		lines = append(lines,
			fmt.Sprintf("  card    %s", d.Claim.Str("id")),
			fmt.Sprintf("  who     %s (%s)  pid %s", d.Claim.Str("actorName"), d.Claim.Str("vendor"), pid),
			fmt.Sprintf("  mode    %s   doing: %s", d.Claim.Str("mode"), task),
			fmt.Sprintf("  scope   %s", strings.Join(d.Claim.Strings("scope.include"), ", ")),
			fmt.Sprintf("  back by %s (in %s)", d.EffectiveExpiresAt, util.HumanMs(d.ExpiresInMs)),
			fmt.Sprintf("  clash   %s: %s", c.Overlap.Reason, strings.Join(hit, ", ")),
			"")
	}
	first := conflicts[0].Holder.Claim
	lines = append(lines,
		"You can:",
		fmt.Sprintf("  %s board                          # see the whole door", brand.Bin),
		fmt.Sprintf("  %s claim <narrower-path> ...      # take a different chore", brand.Bin),
		fmt.Sprintf("  %s wait %s --timeout 10m", brand.Bin, first.Str("id")),
		fmt.Sprintf("  %s handoff %s --to %s --note \"...\"", brand.Bin, first.Str("id"), first.Str("actorName")))
	return strings.Join(lines, "\n")
}

type claimOutcome struct {
	Conflict  bool
	Conflicts []conflict
	Merged    bool
	Claim     jsonx.Obj
	Token     string
}

func cmdClaim(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	actor, session, err := store.RequireActorMutating(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	if err := secrets.Guard(map[string]string{
		"--task": ctx.Flags.Str("task"), "--label": strings.Join(ctx.Flags.List("label"), " "),
	}, ctx.Flags.Bool("allow-secret-like")); err != nil {
		return 0, err
	}
	if len(ctx.Positional) == 0 {
		return 0, errs.New("E_USAGE", "What do you want to claim?").
			WithHint(brand.Bin + " claim \"src/**\" --task \"refactor api\"")
	}
	task := ctx.Flags.Str("task")
	if task == "" && ws.Config.Bool("policy.requireTaskOnClaim") {
		return 0, errs.New("E_USAGE", "A claim needs --task so the others know what you are doing.").
			WithHint(fmt.Sprintf("%s claim \"%s\" --task \"what you are doing\"", brand.Bin, ctx.Positional[0]))
	}
	mode := ctx.Flags.Str("mode")
	if mode == "" {
		mode = "exclusive"
	}
	if mode != "exclusive" && mode != "shared" && mode != "advisory" {
		return 0, errs.New("E_USAGE", fmt.Sprintf("Unknown --mode '%s'.", mode)).
			WithHint("One of: exclusive, shared, advisory")
	}
	maxTTL := int64(ws.Config.Num("lease.maxTtlMs"))
	ttlInput := ctx.Flags.Str("ttl")
	if ttlInput == "" {
		ttlInput = os.Getenv("FRIDGE_TTL")
	}
	var ttlMs int64
	if ttlInput == "" {
		ttlMs = int64(ws.Config.Num("lease.defaultTtlMs"))
	} else {
		ttlMs, err = util.ParseDuration(ttlInput, "ttl")
		if err != nil {
			return 0, err
		}
	}
	if ttlMs > maxTTL {
		ctx.warn(fmt.Sprintf("--ttl capped at %s (lease.maxTtlMs).", util.HumanMs(maxTTL)))
		ttlMs = maxTTL
	}
	if ttlMs < 1000 {
		return 0, errs.New("E_USAGE", "--ttl must be at least 1s.")
	}
	include, err := normalizeAll(ws, ctx.Positional, ctx.Flags.Bool("confirm-global"))
	if err != nil {
		return 0, err
	}
	exclude, err := normalizeAll(ws, ctx.Flags.List("exclude"), false)
	if err != nil {
		return 0, err
	}
	insensitive := paths.DefaultCaseInsensitive(ws.Config.Str("paths.caseSensitivity"))
	var waitMs int64
	if w := ctx.Flags.Str("wait"); w != "" {
		waitMs, err = util.ParseDuration(w, "wait")
		if err != nil {
			return 0, err
		}
	}
	deadline := util.NowMs() + waitMs

	for {
		var result claimOutcome
		err := mutex.With(ws, "claim", func() error {
			if _, e := store.ReapStale(ws, actor, session, false); e != nil {
				return e
			}
			scope, e := buildScope(ws, include, exclude)
			if e != nil {
				return e
			}
			mineScope := scopeFrom(scope)
			existing := []store.Decorated{}
			all, e := store.ListClaimsStrict(ws, true)
			if e != nil {
				return e
			}
			for _, d := range all {
				if !d.Stale && store.IsHeld(d.Claim) {
					existing = append(existing, d)
				}
			}
			conflicts := []conflict{}
			for _, d := range existing {
				if d.Claim.Str("sessionId") == session.Str("id") {
					continue
				}
				if !ModesCollide(mode, d.Claim.Str("mode")) {
					continue
				}
				ov, e := paths.ScopesOverlap(mineScope, scopeFrom(d.Claim.ObjAt("scope")), insensitive)
				if e != nil {
					return e
				}
				if ov.Overlap {
					conflicts = append(conflicts, conflict{Holder: d, Overlap: ov})
				}
			}
			if len(conflicts) > 0 {
				result = claimOutcome{Conflict: true, Conflicts: conflicts}
				return nil
			}
			// Only after nobody else is in the way may I widen a card I already
			// hold. Widening never changes its mode: that would be a new promise
			// to everyone else.
			mineOverlapping := []store.Decorated{}
			for _, d := range existing {
				if d.Claim.Str("sessionId") != session.Str("id") || d.Claim.Str("mode") != mode {
					continue
				}
				ov, e := paths.ScopesOverlap(mineScope, scopeFrom(d.Claim.ObjAt("scope")), insensitive)
				if e != nil {
					return e
				}
				if ov.Overlap {
					mineOverlapping = append(mineOverlapping, d)
				}
			}
			if len(mineOverlapping) > 0 && !ctx.Flags.Bool("strict") {
				target := mineOverlapping[0]
				merged := []string{}
				seen := map[string]bool{}
				for _, p := range append(target.Claim.Strings("scope.include"), include...) {
					if !seen[p] {
						seen[p] = true
						merged = append(merged, p)
					}
				}
				nextScope, e := buildScope(ws, merged, target.Claim.Strings("scope.exclude"))
				if e != nil {
					return e
				}
				nextTask := task
				if nextTask == "" {
					nextTask = target.Claim.Str("task")
				}
				updated := target.Claim.With(jsonx.Obj{"scope": nextScope, "task": nextTask, "updatedAt": util.Now()})
				if e := store.SaveClaim(ws, updated); e != nil {
					return e
				}
				if _, e := store.WriteLease(ws, updated.Str("id"), session.Str("id"), ttlMs,
					int(target.Lease.Num("renewals"))+1); e != nil {
					return e
				}
				result = claimOutcome{Merged: true, Claim: updated}
				return nil
			}
			token := util.RandomToken()
			labels := jsonx.Obj{"branch": currentBranch(ws.Root)}
			for _, l := range ctx.Flags.List("label") {
				if i := strings.Index(l, "="); i == -1 {
					labels[l] = "true"
				} else {
					labels[l[:i]] = l[i+1:]
				}
			}
			var taskVal any
			if task != "" {
				taskVal = task
			}
			record := jsonx.Obj{
				"schema":      "wcp/0.1/claim",
				"id":          util.NewID("clm"),
				"workspaceId": ws.Config.Str("workspaceId"),
				"actorId":     actor.Str("id"),
				"actorName":   actor.Str("name"),
				"vendor":      actor.Str("vendor"),
				"sessionId":   session.Str("id"),
				"host":        util.HostID(),
				"process": jsonx.Obj{
					"pid": float64(os.Getpid()), "ppid": float64(os.Getppid()), "startedAt": util.Now(),
				},
				"mode":             mode,
				"task":             taskVal,
				"labels":           labels,
				"scope":            scope,
				"createdAt":        util.Now(),
				"updatedAt":        util.Now(),
				"ttlMs":            float64(ttlMs),
				"expiresAtInitial": util.NowISO(time.Now().Add(time.Duration(ttlMs) * time.Millisecond)),
				"state":            "active",
				"tokenHash":        util.SHA256(token),
				"writer":           ws.Config.Str("writer"),
			}
			if e := store.SaveClaim(ws, record); e != nil {
				return e
			}
			if _, e := store.WriteLease(ws, record.Str("id"), session.Str("id"), ttlMs, 0); e != nil {
				return e
			}
			tokens := session.ObjAt("tokens")
			if tokens == nil {
				tokens = jsonx.Obj{}
			}
			tokens[record.Str("id")] = token
			session["tokens"] = tokens
			if e := store.WriteSession(ws, session); e != nil {
				return e
			}
			suffix := ""
			if task != "" {
				suffix = fmt.Sprintf(" for \"%s\"", task)
			}
			if _, e := store.Pin(ws, store.PinArgs{
				Type: "claim.acquired", Actor: actor, Session: session,
				Subject: jsonx.Obj{"kind": "claim", "id": record.Str("id")},
				Summary: fmt.Sprintf("%s took %s%s", actor.Str("name"), strings.Join(include, ", "), suffix),
				Data: jsonx.Obj{"mode": mode, "ttlMs": float64(ttlMs), "include": strArr(include),
					"exclude": strArr(exclude), "files": float64(len(scope.ArrAt("materialized")))},
			}); e != nil {
				return e
			}
			result = claimOutcome{Claim: record, Token: token}
			return nil
		}, nil)
		if err != nil {
			return 0, err
		}

		if !result.Conflict {
			c := result.Claim
			render.Auto(ws)
			var text string
			if result.Merged {
				text = fmt.Sprintf("Extended your card %s -> %s", c.Str("id"), strings.Join(c.Strings("scope.include"), ", "))
			} else {
				minus := ""
				if ex := c.Strings("scope.exclude"); len(ex) > 0 {
					minus = fmt.Sprintf("  (minus %s)", strings.Join(ex, ", "))
				}
				trunc := ""
				if c.Bool("scope.materializedTruncated") {
					trunc = "+ (truncated, treated conservatively)"
				}
				text = strings.Join([]string{
					fmt.Sprintf("Card %s is yours.", c.Str("id")),
					fmt.Sprintf("  scope    %s%s", strings.Join(c.Strings("scope.include"), ", "), minus),
					fmt.Sprintf("  files    %d%s", len(c.ArrAt("scope.materialized")), trunc),
					fmt.Sprintf("  mode     %s", c.Str("mode")),
					fmt.Sprintf("  back by  %s from now", util.HumanMs(int64(c.Num("ttlMs")))),
					"",
					fmt.Sprintf("When you stop: %s release %s --outcome done --note \"what changed\"", brand.Bin, c.Str("id")),
				}, "\n")
			}
			var tok any
			if result.Token != "" {
				tok = result.Token
			}
			return ctx.emit("claim", output.Result{
				Data: jsonx.Obj{
					"claimId": c.Str("id"), "merged": result.Merged, "mode": c.Str("mode"), "task": c.Get("task"),
					"ttlMs": c.Num("ttlMs"), "scope": c.ObjAt("scope"), "expiresAt": c.Str("expiresAtInitial"),
					"token": tok,
				},
				Text: text,
			})
		}

		if util.NowMs() < deadline {
			step := waitMs / 20
			if step < 200 {
				step = 200
			}
			if step > 1000 {
				step = 1000
			}
			util.Sleep(util.Jitter(step, 0.3))
			continue
		}

		blocked := jsonx.Arr{}
		for _, c := range result.Conflicts {
			blocked = append(blocked, jsonx.Obj{
				"claimId": c.Holder.Claim.Str("id"), "actor": c.Holder.Claim.Str("actorName"),
				"reason": c.Overlap.Reason,
			})
		}
		if _, err := store.Pin(ws, store.PinArgs{
			Type: "claim.denied", Actor: actor, Session: session,
			Summary: fmt.Sprintf("%s was blocked on %s", actor.Str("name"), strings.Join(include, ", ")),
			Data:    jsonx.Obj{"include": strArr(include), "blockedBy": blocked},
		}); err != nil {
			return 0, err
		}
		queued := []string{}
		if ctx.Flags.Bool("queue") {
			waitingFor := jsonx.Arr{}
			ids := []string{}
			for _, c := range result.Conflicts {
				var taskVal any
				if t := ctx.Flags.Str("task"); t != "" {
					taskVal = t
				}
				entry, err := store.WriteQueueEntry(ws, jsonx.Obj{
					"id": util.NewID("que"), "claimId": c.Holder.Claim.Str("id"),
					"actorName": actor.Str("name"), "sessionId": session.Str("id"),
					"include": strArr(include), "task": taskVal,
				})
				if err != nil {
					return 0, err
				}
				queued = append(queued, entry.Str("id"))
				waitingFor = append(waitingFor, entry.Str("claimId"))
				ids = append(ids, c.Holder.Claim.Str("id"))
			}
			if _, err := store.Pin(ws, store.PinArgs{
				Type: "queue.joined", Actor: actor, Session: session,
				Summary: fmt.Sprintf("%s is waiting for %s", actor.Str("name"), strings.Join(ids, ", ")),
				Data:    jsonx.Obj{"include": strArr(include), "waitingFor": waitingFor},
			}); err != nil {
				return 0, err
			}
		}
		render.Auto(ws)
		first := result.Conflicts[0].Holder.Claim
		if waitMs > 0 {
			return 0, errs.New("E_WAIT_TIMEOUT", fmt.Sprintf("Still claimed after %s.", util.HumanMs(waitMs))).
				WithHint(brand.Bin + " board").
				WithDetails(jsonx.Obj{"report": conflictReport(result.Conflicts), "queued": strArr(queued)})
		}
		report := conflictReport(result.Conflicts)
		if len(queued) > 0 {
			report += fmt.Sprintf("\n\nYou are on the waiting list (%d card(s)). %s wait %s",
				len(queued), brand.Bin, first.Str("id"))
		}
		details := jsonx.Arr{}
		for _, c := range result.Conflicts {
			details = append(details, jsonx.Obj{
				"claimId": c.Holder.Claim.Str("id"), "actorName": c.Holder.Claim.Str("actorName"),
				"vendor": c.Holder.Claim.Str("vendor"), "mode": c.Holder.Claim.Str("mode"),
				"task": c.Holder.Claim.Get("task"), "include": strArr(c.Holder.Claim.Strings("scope.include")),
				"expiresAt": c.Holder.EffectiveExpiresAt, "reason": c.Overlap.Reason,
				"paths": strArr(c.Overlap.Paths),
			})
		}
		return 0, errs.New("E_CONFLICT", fmt.Sprintf("%d card(s) already cover those paths.", len(result.Conflicts))).
			WithHint(fmt.Sprintf("%s board  |  %s wait %s  |  %s handoff ...", brand.Bin, brand.Bin, first.Str("id"), brand.Bin)).
			WithDetails(jsonx.Obj{"report": report, "queued": strArr(queued), "conflicts": details})
	}
}

func classify(ws *store.Workspace, session jsonx.Obj, queries []string) (jsonx.Arr, error) {
	insensitive := paths.DefaultCaseInsensitive(ws.Config.Str("paths.caseSensitivity"))
	active := []store.Decorated{}
	all, err := store.ListClaimsStrict(ws, true)
	if err != nil {
		return nil, err
	}
	for _, d := range all {
		if !d.Stale && store.IsHeld(d.Claim) {
			active = append(active, d)
		}
	}
	rows := jsonx.Arr{}
	for _, pattern := range queries {
		scope, err := buildScope(ws, []string{pattern}, nil)
		if err != nil {
			return nil, err
		}
		mine := []conflict{}
		theirs := []conflict{}
		for _, d := range active {
			ov, err := paths.ScopesOverlap(scopeFrom(scope), scopeFrom(d.Claim.ObjAt("scope")), insensitive)
			if err != nil {
				return nil, err
			}
			if !ov.Overlap {
				continue
			}
			if session != nil && d.Claim.Str("sessionId") == session.Str("id") {
				mine = append(mine, conflict{d, ov})
			} else if d.Claim.Str("mode") != "advisory" {
				theirs = append(theirs, conflict{d, ov})
			}
		}
		status := "unclaimed"
		if len(theirs) > 0 {
			status = "theirs"
		} else if len(mine) > 0 {
			status = "yours"
		}
		var claimID, actorName, mode, expiresAt, reason any
		if len(mine) > 0 {
			claimID = mine[0].Holder.Claim.Str("id")
		} else if len(theirs) > 0 {
			claimID = theirs[0].Holder.Claim.Str("id")
		}
		pick := theirs
		if len(pick) == 0 {
			pick = mine
		}
		if len(pick) > 0 {
			actorName = pick[0].Holder.Claim.Str("actorName")
			mode = pick[0].Holder.Claim.Str("mode")
			expiresAt = pick[0].Holder.EffectiveExpiresAt
			reason = pick[0].Overlap.Reason
		}
		rows = append(rows, jsonx.Obj{
			"path": pattern, "status": status, "claimId": claimID, "actorName": actorName,
			"mode": mode, "expiresAt": expiresAt, "reason": reason,
		})
	}
	return rows, nil
}

func verdict(ctx *Ctx, command string, rows jsonx.Arr, requireClaim bool) (int, error) {
	theirs := 0
	unclaimed := jsonx.Arr{}
	lines := []string{}
	for _, raw := range rows {
		r, _ := raw.(jsonx.Obj)
		switch r.Str("status") {
		case "theirs":
			theirs++
		case "unclaimed":
			unclaimed = append(unclaimed, r)
		}
		tag := "unclaimed"
		switch r.Str("status") {
		case "yours":
			tag = "yours    "
		case "theirs":
			tag = "THEIRS   "
		}
		who := ""
		if r.Str("actorName") != "" {
			who = fmt.Sprintf("  (%s, %s)", r.Str("actorName"), r.Str("claimId"))
		}
		lines = append(lines, tag+" "+r.Str("path")+who)
	}
	text := strings.Join(lines, "\n")
	if theirs > 0 {
		return 0, errs.New("E_CONFLICT", fmt.Sprintf("%d path(s) belong to somebody else.", theirs)).
			WithHint(brand.Bin + " board").
			WithDetails(jsonx.Obj{"report": text, "paths": rows})
	}
	if len(unclaimed) > 0 && requireClaim {
		firstObj, _ := unclaimed[0].(jsonx.Obj)
		return 0, errs.New("E_OUT_OF_SCOPE", fmt.Sprintf("%d path(s) are outside any card you hold.", len(unclaimed))).
			WithHint(fmt.Sprintf("%s claim \"%s\" --task \"...\"", brand.Bin, firstObj.Str("path"))).
			WithDetails(jsonx.Obj{"report": text, "paths": rows})
	}
	if text == "" {
		text = "nothing to check"
	}
	return ctx.emit(command, output.Result{Data: jsonx.Obj{"paths": rows, "ok": true}, Text: text})
}

func cmdCheck(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	_, session, err := store.RequireActor(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	if _, err := maybeRenew(ws, session); err != nil {
		return 0, err
	}
	if len(ctx.Positional) == 0 {
		return 0, errs.New("E_USAGE", "Which paths?").WithHint(brand.Bin + " check src/api/routes.ts")
	}
	queries, err := normalizeAll(ws, ctx.Positional, true)
	if err != nil {
		return 0, err
	}
	rows, err := classify(ws, session, queries)
	if err != nil {
		return 0, err
	}
	return verdict(ctx, "check", rows, ctx.Flags.Bool("for-write"))
}

func cmdGuard(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	_, session, err := store.RequireActor(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	if _, err := maybeRenew(ws, session); err != nil {
		return 0, err
	}
	inputs := ctx.Positional
	if ctx.Flags.Bool("staged") {
		out, err := exec.Command("git", "-C", ws.Root, "diff", "--cached", "--name-only", "-z").Output()
		if err != nil {
			return 0, errs.New("E_USAGE", "git diff --cached failed; is this a git repository?")
		}
		inputs = nil
		for _, s := range strings.Split(string(out), "\x00") {
			if s != "" {
				inputs = append(inputs, s)
			}
		}
	}
	if len(inputs) == 0 {
		return ctx.emit("guard", output.Result{
			Data: jsonx.Obj{"paths": jsonx.Arr{}, "ok": true}, Text: "nothing staged, nothing to guard",
		})
	}
	queries, err := normalizeAll(ws, inputs, true)
	if err != nil {
		return 0, err
	}
	rows, err := classify(ws, session, queries)
	if err != nil {
		return 0, err
	}
	return verdict(ctx, "guard", rows, ws.Config.Str("policy.requireClaimForWrite") == "strict")
}

func cmdHeartbeat(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	_, session, err := store.RequireActorMutating(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	ids := ctx.Flags.List("claim")
	mine := []store.Decorated{}
	for _, d := range store.ListClaims(ws, true) {
		if d.Claim.Str("sessionId") == session.Str("id") {
			mine = append(mine, d)
		}
	}
	targets := mine
	if len(ids) > 0 {
		targets = nil
		for _, d := range mine {
			for _, id := range ids {
				if d.Claim.Str("id") == id {
					targets = append(targets, d)
					break
				}
			}
		}
		if len(targets) != len(ids) {
			missing := []string{}
			for _, id := range ids {
				found := false
				for _, d := range targets {
					if d.Claim.Str("id") == id {
						found = true
					}
				}
				if !found {
					missing = append(missing, id)
				}
			}
			return 0, errs.New("E_NOT_FOUND", fmt.Sprintf("No live card of yours named %s.", strings.Join(missing, ", "))).
				WithHint(brand.Bin + " whoami")
		}
	}
	if len(targets) == 0 {
		return ctx.emit("heartbeat", output.Result{
			Data: jsonx.Obj{"renewed": jsonx.Arr{}}, Text: "you are not holding any cards",
		})
	}
	expired := []string{}
	for _, d := range targets {
		if d.Stale {
			expired = append(expired, d.Claim.Str("id"))
		}
	}
	if len(expired) > 0 {
		return 0, errs.New("E_LEASE_EXPIRED", fmt.Sprintf("%d of your card(s) already fell off the door.", len(expired))).
			WithHint(fmt.Sprintf("%s reap && %s claim ... again", brand.Bin, brand.Bin)).
			WithDetails(jsonx.Obj{"claims": strArr(expired)})
	}
	var ttlOverride int64
	if t := ctx.Flags.Str("ttl"); t != "" {
		ttlOverride, err = util.ParseDuration(t, "ttl")
		if err != nil {
			return 0, err
		}
	}
	maxTTL := int64(ws.Config.Num("lease.maxTtlMs"))
	renewed := jsonx.Arr{}
	lines := []string{}
	for _, d := range targets {
		ttl := ttlOverride
		if ttl == 0 {
			ttl = int64(d.Claim.Num("ttlMs"))
		}
		if ttl > maxTTL {
			ttl = maxTTL
		}
		lease, err := store.WriteLease(ws, d.Claim.Str("id"), session.Str("id"), ttl, int(d.Lease.Num("renewals"))+1)
		if err != nil {
			return 0, err
		}
		renewed = append(renewed, jsonx.Obj{"claimId": d.Claim.Str("id"), "expiresAt": lease.Str("expiresAt")})
		lines = append(lines, fmt.Sprintf("  %s  until %s", d.Claim.Str("id"), lease.Str("expiresAt")))
	}
	render.Auto(ws)
	return ctx.emit("heartbeat", output.Result{
		Data: jsonx.Obj{"renewed": renewed},
		Text: fmt.Sprintf("still on it: renewed %d card(s)\n%s", len(renewed), strings.Join(lines, "\n")),
	})
}

func cmdExtend(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	_, session, err := store.RequireActorMutating(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	if len(ctx.Positional) == 0 {
		return 0, errs.New("E_USAGE", "Which card?").WithHint(brand.Bin + " extend <claim-id> --ttl 1h")
	}
	id := ctx.Positional[0]
	if ctx.Flags.Str("ttl") == "" {
		return 0, errs.New("E_USAGE", "--ttl is required.").WithHint(fmt.Sprintf("%s extend %s --ttl 1h", brand.Bin, id))
	}
	d, err := store.ReadClaim(ws, id)
	if err != nil {
		return 0, err
	}
	if d == nil {
		return 0, errs.New("E_NOT_FOUND", fmt.Sprintf("No card %s.", id)).WithHint(brand.Bin + " board")
	}
	if d.Claim.Str("sessionId") != session.Str("id") {
		return 0, errs.New("E_NOT_OWNER", fmt.Sprintf("Card %s belongs to %s.", id, d.Claim.Str("actorName"))).
			WithHint(fmt.Sprintf("%s handoff %s --to <you>", brand.Bin, id))
	}
	ttlMs, err := util.ParseDuration(ctx.Flags.Str("ttl"), "ttl")
	if err != nil {
		return 0, err
	}
	if maxTTL := int64(ws.Config.Num("lease.maxTtlMs")); ttlMs > maxTTL {
		ttlMs = maxTTL
	}
	if err := store.SaveClaim(ws, d.Claim.With(jsonx.Obj{"ttlMs": float64(ttlMs), "updatedAt": util.Now()})); err != nil {
		return 0, err
	}
	lease, err := store.WriteLease(ws, id, session.Str("id"), ttlMs, int(d.Lease.Num("renewals"))+1)
	if err != nil {
		return 0, err
	}
	render.Auto(ws)
	return ctx.emit("extend", output.Result{
		Data: jsonx.Obj{"claimId": id, "ttlMs": float64(ttlMs), "expiresAt": lease.Str("expiresAt")},
		Text: fmt.Sprintf("%s now runs until %s", id, lease.Str("expiresAt")),
	})
}

func cmdRelease(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	actor, session, err := store.RequireActorMutating(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	if err := secrets.Guard(map[string]string{"--note": ctx.Flags.Str("note")},
		ctx.Flags.Bool("allow-secret-like")); err != nil {
		return 0, err
	}
	all := ctx.Flags.Bool("all")
	ids := ctx.Positional
	if !all && len(ids) == 0 {
		return 0, errs.New("E_USAGE", "Which card?").
			WithHint(fmt.Sprintf("%s release <claim-id> | %s release --all", brand.Bin, brand.Bin))
	}
	outcome := ctx.Flags.Str("outcome")
	if outcome == "" {
		outcome = "done"
	}
	if outcome != "done" && outcome != "partial" && outcome != "abandoned" && outcome != "failed" {
		return 0, errs.New("E_USAGE", fmt.Sprintf("Unknown --outcome '%s'.", outcome)).
			WithHint("One of: done, partial, abandoned, failed")
	}
	released := jsonx.Arr{}
	lines := []string{}
	var inner error
	err = mutex.With(ws, "release", func() error {
		mine := []store.Decorated{}
		for _, d := range store.ListClaims(ws, true) {
			if d.Claim.Str("sessionId") == session.Str("id") {
				mine = append(mine, d)
			}
		}
		targets := mine
		if !all {
			targets = nil
			for _, id := range ids {
				d, e := store.ReadClaim(ws, id)
				if e != nil {
					return e
				}
				if d == nil {
					inner = errs.New("E_NOT_FOUND", fmt.Sprintf("No card %s.", id)).WithHint(brand.Bin + " board")
					return nil
				}
				token := session.ObjAt("tokens").Str(id)
				owns := d.Claim.Str("sessionId") == session.Str("id") ||
					(token != "" && util.SHA256(token) == d.Claim.Str("tokenHash"))
				if !owns && !ctx.Flags.Bool("force") {
					inner = errs.New("E_NOT_OWNER",
						fmt.Sprintf("Card %s belongs to %s (%s).", id, d.Claim.Str("actorName"), d.Claim.Str("vendor"))).
						WithHint(fmt.Sprintf("%s handoff %s --to %s   (or --force if a human told you to)", brand.Bin, id, actor.Str("name")))
					return nil
				}
				if d.Stale && !ctx.Flags.Bool("force") {
					inner = errs.New("E_LEASE_EXPIRED", fmt.Sprintf("Card %s already fell off the door.", id)).
						WithHint(brand.Bin + " reap")
					return nil
				}
				if !owns && d.Claim.Str("host") != util.HostID() && !ctx.Flags.Bool("allow-multihost") {
					inner = errs.New("E_FOREIGN_HOST",
						fmt.Sprintf("Card %s was taken on another machine, so its owner's liveness cannot be checked from here.", id)).
						WithHint(fmt.Sprintf("%s release %s --force --allow-multihost   (only if you know that machine is done)", brand.Bin, id)).
						WithDetails(jsonx.Obj{"claimId": id, "host": d.Claim.Str("host"), "thisHost": util.HostID()})
					return nil
				}
				targets = append(targets, *d)
			}
		}
		for _, d := range targets {
			// The wire format distinguishes a card its owner took down from
			// one an operator took away. Forcing somebody else's card is
			// `revoked`.
			finalState := "released"
			if ctx.Flags.Bool("force") && d.Claim.Str("sessionId") != session.Str("id") {
				finalState = "revoked"
			}
			if _, e := store.ArchiveClaim(ws, d.Claim, finalState); e != nil {
				return e
			}
			if tokens := session.ObjAt("tokens"); tokens != nil {
				delete(tokens, d.Claim.Str("id"))
			}
			noteSuffix := ""
			if n := ctx.Flags.Str("note"); n != "" {
				noteSuffix = ": " + n
			}
			createdMs, _ := util.ParseMs(d.Claim.Str("createdAt"))
			var noteVal any
			if n := ctx.Flags.Str("note"); n != "" {
				noteVal = n
			}
			if _, e := store.Pin(ws, store.PinArgs{
				Type: "claim.released", Actor: actor, Session: session,
				Subject: jsonx.Obj{"kind": "claim", "id": d.Claim.Str("id")},
				Summary: fmt.Sprintf("%s finished %s (%s)%s", actor.Str("name"),
					strings.Join(d.Claim.Strings("scope.include"), ", "), outcome, noteSuffix),
				Data: jsonx.Obj{"outcome": outcome, "note": noteVal, "forced": ctx.Flags.Bool("force"),
					"heldMs": float64(util.NowMs() - createdMs), "include": strArr(d.Claim.Strings("scope.include"))},
			}); e != nil {
				return e
			}
			released = append(released, jsonx.Obj{
				"claimId": d.Claim.Str("id"), "include": strArr(d.Claim.Strings("scope.include")), "outcome": outcome,
			})
			lines = append(lines, fmt.Sprintf("  %s  %s", d.Claim.Str("id"), strings.Join(d.Claim.Strings("scope.include"), ", ")))
		}
		return store.WriteSession(ws, session)
	}, nil)
	if err != nil {
		return 0, err
	}
	if inner != nil {
		return 0, inner
	}
	render.Auto(ws)
	text := "you were not holding any cards"
	if len(released) > 0 {
		text = fmt.Sprintf("took down %d card(s):\n%s", len(released), strings.Join(lines, "\n"))
	}
	return ctx.emit("release", output.Result{
		Data: jsonx.Obj{"released": released, "outcome": outcome}, Text: text,
	})
}

func cmdReap(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	var actor, session jsonx.Obj
	if a, s, err := store.RequireActor(ws, ctx.Flags.Str("agent"), ""); err == nil {
		actor, session = a, s
	}
	force := ctx.Flags.Bool("force")
	targets := func() []store.Decorated {
		out := []store.Decorated{}
		for _, d := range store.ListClaims(ws, true) {
			if d.Stale || (force && d.Expired) {
				out = append(out, d)
			}
		}
		return out
	}
	if ctx.Flags.Bool("dry-run") {
		stale := targets()
		rows := jsonx.Arr{}
		lines := []string{}
		for _, d := range stale {
			rows = append(rows, jsonx.Obj{"claimId": d.Claim.Str("id"), "actorName": d.Claim.Str("actorName"),
				"expiredAt": d.EffectiveExpiresAt})
			lines = append(lines, fmt.Sprintf("would sweep %s (%s, expired %s)",
				d.Claim.Str("id"), d.Claim.Str("actorName"), d.EffectiveExpiresAt))
		}
		text := "nothing has fallen off the door"
		if len(lines) > 0 {
			text = strings.Join(lines, "\n")
		}
		return ctx.emit("reap", output.Result{
			Data: jsonx.Obj{"dryRun": true, "force": force, "wouldReap": rows}, Text: text,
		})
	}
	if force && !ctx.Flags.Bool("allow-multihost") {
		foreign := []string{}
		for _, d := range targets() {
			if d.Claim.Str("host") != util.HostID() {
				foreign = append(foreign, d.Claim.Str("id"))
			}
		}
		if len(foreign) > 0 {
			return 0, errs.New("E_FOREIGN_HOST", fmt.Sprintf("%d card(s) were taken on another machine.", len(foreign))).
				WithHint(brand.Bin + " reap --force --allow-multihost   (only if you know that machine is done)").
				WithDetails(jsonx.Obj{"claimIds": strArr(foreign), "thisHost": util.HostID()})
		}
	}
	var reaped []jsonx.Obj
	err = mutex.With(ws, "reap", func() error {
		r, e := store.ReapStale(ws, actor, session, force)
		reaped = r
		return e
	}, nil)
	if err != nil {
		return 0, err
	}
	render.Auto(ws)
	rows := jsonx.Arr{}
	lines := []string{}
	for _, c := range reaped {
		rows = append(rows, jsonx.Obj{"claimId": c.Str("id"), "actorName": c.Str("actorName"),
			"include": strArr(c.Strings("scope.include"))})
		lines = append(lines, fmt.Sprintf("  %s  %s  %s", c.Str("id"), c.Str("actorName"),
			strings.Join(c.Strings("scope.include"), ", ")))
	}
	text := "nothing has fallen off the door"
	if len(reaped) > 0 {
		text = fmt.Sprintf("swept %d fallen card(s):\n%s", len(reaped), strings.Join(lines, "\n"))
	}
	return ctx.emit("reap", output.Result{Data: jsonx.Obj{"forced": force, "reaped": rows}, Text: text})
}

func cmdWait(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	if len(ctx.Positional) == 0 {
		return 0, errs.New("E_USAGE", "Wait for which card?").WithHint(brand.Bin + " wait <claim-id> --timeout 10m")
	}
	id := ctx.Positional[0]
	timeoutInput := ctx.Flags.Str("timeout")
	if timeoutInput == "" {
		timeoutInput = "10m"
	}
	timeoutMs, err := util.ParseDuration(timeoutInput, "timeout")
	if err != nil {
		return 0, err
	}
	started := util.NowMs()
	initial, err := store.ReadClaim(ws, id)
	if err != nil {
		return 0, err
	}
	if initial == nil {
		return 0, errs.New("E_NOT_FOUND", fmt.Sprintf("No card %s. It may already be gone.", id)).
			WithHint(brand.Bin + " board")
	}
	// Put a marker on the waiting list so the board can show who is blocked.
	entryID := ""
	if actor, session, err := store.RequireActor(ws, ctx.Flags.Str("agent"), ""); err == nil {
		if entry, err := store.WriteQueueEntry(ws, jsonx.Obj{
			"id": util.NewID("que"), "claimId": id, "actorName": actor.Str("name"),
			"sessionId": session.Str("id"), "include": strArr(initial.Claim.Strings("scope.include")), "task": nil,
		}); err == nil {
			entryID = entry.Str("id")
		}
	}
	defer func() {
		if entryID != "" {
			store.RemoveQueueEntry(ws, entryID)
		}
	}()
	for {
		d, err := store.ReadClaim(ws, id)
		if err != nil {
			return 0, err
		}
		if d == nil || d.Stale {
			waited := util.NowMs() - started
			return ctx.emit("wait", output.Result{
				Data: jsonx.Obj{"claimId": id, "waitedMs": float64(waited), "gone": d == nil,
					"stale": d != nil && d.Stale},
				Text: fmt.Sprintf("card %s is gone after %s", id, util.HumanMs(waited)),
			})
		}
		if util.NowMs()-started > timeoutMs {
			return 0, errs.New("E_WAIT_TIMEOUT", fmt.Sprintf("Card %s is still held after %s.", id, util.HumanMs(timeoutMs))).
				WithHint(fmt.Sprintf("%s handoff %s --to <you> --note \"can I take this?\"", brand.Bin, id)).
				WithDetails(jsonx.Obj{"claimId": id, "actorName": d.Claim.Str("actorName"), "expiresAt": d.EffectiveExpiresAt})
		}
		util.Sleep(util.Jitter(500, 0.3))
	}
}

func cmdRun(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	if len(ctx.Rest) == 0 {
		return 0, errs.New("E_USAGE", "Nothing to run.").
			WithHint(brand.Bin + " run --claim \"src/**\" --task \"tests\" -- npm test")
	}
	claimPaths := ctx.Flags.List("claim")
	if len(claimPaths) == 0 {
		return 0, errs.New("E_USAGE", "--claim <path> is required.").
			WithHint(brand.Bin + " run --claim \"src/**\" -- npm test")
	}
	task := ctx.Flags.Str("task")
	if task == "" {
		task = sliceStr(strings.Join(ctx.Rest, " "), 120)
	}
	claimFlags := Flags{}
	for k, v := range ctx.Flags {
		claimFlags[k] = v
	}
	claimFlags["task"] = task
	var buf bytes.Buffer
	claimCtx := &Ctx{
		Command: "claim", Positional: claimPaths, Flags: claimFlags, Rest: ctx.Rest,
		JSON: true, Quiet: true, Cwd: ctx.Cwd,
		Out: &output.Ctx{JSON: true, Quiet: true, Stdout: &buf, Stderr: ctx.Out.Stderr},
	}
	if _, err := cmdClaim(claimCtx); err != nil {
		return 0, err
	}
	claimID := ""
	if env, err := jsonx.ParseObj(buf.Bytes()); err == nil {
		claimID = env.Str("data.claimId")
	}

	ttlInput := ctx.Flags.Str("ttl")
	var ttlMs int64
	if ttlInput == "" {
		ttlMs = int64(ws.Config.Num("lease.defaultTtlMs"))
	} else {
		ttlMs, err = util.ParseDuration(ttlInput, "ttl")
		if err != nil {
			return 0, err
		}
	}
	beat := ttlMs / 3
	if beat < 1000 {
		beat = 1000
	}
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Duration(beat) * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				ws2, err := open(ctx)
				if err != nil {
					continue
				}
				_, session, err := store.RequireActorMutating(ws2, ctx.Flags.Str("agent"), "")
				if err != nil {
					continue
				}
				// The child matters more than the heartbeat.
				_, _ = store.WriteLease(ws2, claimID, session.Str("id"), ttlMs, 0)
			case <-stop:
				return
			}
		}
	}()

	code := 0
	// Resolve before spawning so a command that never started can say so.
	// On Windows npm is npm.cmd, and LookPath is what consults PATHEXT; a
	// bare 127 with no explanation is the worst possible answer here.
	target := ctx.Rest[0]
	if resolved, lookErr := exec.LookPath(target); lookErr == nil {
		target = resolved
	}
	child := exec.Command(target, ctx.Rest[1:]...)
	child.Dir = ctx.Cwd
	child.Stdin = os.Stdin
	child.Stdout = os.Stdout
	child.Stderr = os.Stderr
	if err := child.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
			if code < 0 {
				code = 128 + signalNumber(ee)
			}
		} else {
			// A command that never started is not a command that failed.
			// Say which, and say why, instead of returning a bare 127.
			if isNotFound(err) {
				code = 127
			} else {
				code = 1
			}
			fmt.Fprintf(ctx.Out.Stderr, "%s run: could not start '%s': %v\n", brand.Bin, ctx.Rest[0], err)
			if runtime.GOOS == "windows" {
				pathext := os.Getenv("PATHEXT")
				if pathext == "" {
					pathext = ".COM;.EXE;.BAT;.CMD"
				}
				fmt.Fprintf(ctx.Out.Stderr, "  looked for '%s' using PATHEXT (%s)\n", target, pathext)
			}
		}
	}
	close(stop)

	if code != 0 && ctx.Flags.Bool("keep-on-failure") {
		fmt.Fprintf(ctx.Out.Stderr, "command exited %d; keeping card %s (--keep-on-failure)\n", code, claimID)
		return code, nil
	}
	releaseFlags := Flags{}
	for k, v := range ctx.Flags {
		releaseFlags[k] = v
	}
	releaseFlags["outcome"] = "done"
	if code != 0 {
		releaseFlags["outcome"] = "failed"
	}
	releaseFlags["note"] = fmt.Sprintf("%s exited %d", ctx.Rest[0], code)
	releaseCtx := &Ctx{
		Command: "release", Positional: []string{claimID}, Flags: releaseFlags, Rest: ctx.Rest,
		JSON: ctx.JSON, Quiet: true, Cwd: ctx.Cwd,
		Out: &output.Ctx{JSON: ctx.JSON, Quiet: true, Stdout: ctx.Out.Stdout, Stderr: ctx.Out.Stderr},
	}
	if _, err := cmdRelease(releaseCtx); err != nil {
		return 0, err
	}
	return code, nil
}

func isNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "executable file not found")
}
