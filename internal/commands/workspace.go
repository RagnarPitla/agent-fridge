// SPDX-License-Identifier: Apache-2.0
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/RagnarPitla/agent-fridge/internal/adapters"
	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/fsx"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/output"
	"github.com/RagnarPitla/agent-fridge/internal/render"
	"github.com/RagnarPitla/agent-fridge/internal/store"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

var vendorValues = []string{"claude", "copilot", "codex", "cursor", "human", "other"}

func open(ctx *Ctx) (*store.Workspace, error) {
	return store.Open(store.OpenOptions{Repo: ctx.Flags.Str("repo"), Cwd: ctx.Cwd, RequireInit: true})
}

func cmdInit(ctx *Ctx) (int, error) {
	pre, err := store.Open(store.OpenOptions{Repo: ctx.Flags.Str("repo"), Cwd: ctx.Cwd, RequireInit: false})
	if err != nil {
		return 0, err
	}
	created, _, err := store.Init(pre.Root, ctx.Flags.Bool("force"))
	if err != nil {
		return 0, err
	}
	root := created.Root
	ws, err := store.Open(store.OpenOptions{Repo: root, Cwd: ctx.Cwd, RequireInit: true})
	if err != nil {
		return 0, err
	}
	if ctx.Flags.Has("commit-notes") {
		if err := ws.Config.Set("notes.commit", ctx.Flags.Str("commit-notes") != "false"); err != nil {
			return 0, err
		}
		if err := fsx.WriteJSONAtomic(ws.Paths.Config, ws.Config, ws.Paths.Tmp); err != nil {
			return 0, err
		}
	}
	gitattributes := store.EnsureGitAttributes(root)
	installed := []adapters.Status{}
	if !ctx.Flags.Bool("no-adapters") {
		installed, err = adapters.Install(root, []string{"agents"}, false, ws.Paths.Tmp)
		if err != nil {
			return 0, err
		}
	}
	if _, err := store.Pin(ws, store.PinArgs{
		Type: "workspace.initialized", Summary: "fridge hung on the door at " + root,
		Data: jsonx.Obj{"root": root, "protocol": brand.Protocol, "version": brand.Version},
	}); err != nil {
		return 0, err
	}
	if err := fsx.WriteAtomic(ws.Paths.Door, render.Door(ws), ws.Paths.Tmp); err != nil {
		return 0, err
	}
	parts := []string{
		fmt.Sprintf("The fridge is on the wall.  %s", filepath.Join(root, brand.StateDir)),
		fmt.Sprintf("  protocol      %s", brand.Protocol),
		fmt.Sprintf("  git           %s/.gitignore keeps live state local; notes/ and actors/ are shared history", brand.StateDir),
	}
	if gitattributes {
		parts = append(parts, "  gitattributes .gitattributes updated (notes are never auto-merged)")
	}
	if len(installed) > 0 {
		bits := []string{}
		for _, r := range installed {
			bits = append(bits, fmt.Sprintf("%s (%s)", r.File, r.Action))
		}
		parts = append(parts, "  instructions  "+strings.Join(bits, ", "))
	}
	parts = append(parts,
		"\nNext:",
		fmt.Sprintf("  %s join --agent \"your-name\" --vendor human", brand.Bin),
		fmt.Sprintf("  %s claim \"src/**\" --task \"what you are doing\"", brand.Bin),
		fmt.Sprintf("  %s board", brand.Bin))
	return ctx.emit("init", output.Result{
		Data: jsonx.Obj{
			"root": root, "stateDir": filepath.Join(root, brand.StateDir), "protocol": brand.Protocol,
			"adapters": adapterArr(installed), "gitattributes": gitattributes,
		},
		Text: strings.Join(parts, "\n"),
	})
}

func adapterArr(list []adapters.Status) jsonx.Arr {
	out := jsonx.Arr{}
	for _, r := range list {
		var found any
		if r.FoundHash != "" {
			found = r.FoundHash
		}
		out = append(out, jsonx.Obj{
			"vendor": r.Vendor, "file": filepath.ToSlash(r.File), "absPath": r.AbsPath, "label": r.Label,
			"state": r.State, "foundHash": found, "wantHash": r.WantHash, "action": r.Action,
		})
	}
	return out
}

func cmdJoin(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	name := ctx.Flags.Str("agent")
	if name == "" {
		name = os.Getenv("FRIDGE_ACTOR")
	}
	if name == "" && len(ctx.Positional) > 0 {
		name = ctx.Positional[0]
	}
	if name == "" {
		return 0, errs.New("E_USAGE", "Who are you? Pass --agent <name>.").
			WithHint(brand.Bin + " join --agent \"claude-a\" --vendor claude")
	}
	vendor := ctx.Flags.Str("vendor")
	if vendor == "" {
		vendor = "other"
	}
	known := false
	for _, v := range vendorValues {
		if v == vendor {
			known = true
		}
	}
	if !known {
		return 0, errs.New("E_USAGE", fmt.Sprintf("Unknown --vendor '%s'.", vendor)).
			WithHint("One of: " + strings.Join(vendorValues, ", "))
	}
	res, err := store.JoinActor(ws, name, vendor)
	if err != nil {
		return 0, err
	}
	noteType := "session.started"
	word := "walked up to"
	if res.Resumed {
		noteType = "session.resumed"
		word = "came back to"
	}
	if _, err := store.Pin(ws, store.PinArgs{
		Type: noteType, Actor: res.Actor, Session: res.Session,
		Subject: jsonx.Obj{"kind": "session", "id": res.Session.Str("id")},
		Summary: fmt.Sprintf("%s (%s) %s the fridge", name, vendor, word),
		Data:    jsonx.Obj{"pid": float64(os.Getpid()), "vendor": vendor},
	}); err != nil {
		return 0, err
	}
	render.Auto(ws)
	resumedTag := ""
	if res.Resumed {
		resumedTag = " (resumed)"
	}
	text := strings.Join([]string{
		fmt.Sprintf("Your name is on the door: %s (%s)", res.Actor.Str("name"), res.Actor.Str("vendor")),
		fmt.Sprintf("  actor    %s", res.Actor.Str("id")),
		fmt.Sprintf("  session  %s%s", res.Session.Str("id"), resumedTag),
		"",
		fmt.Sprintf("Tip: export FRIDGE_ACTOR=\"%s\" so you can drop --agent from every command.", res.Actor.Str("name")),
	}, "\n")
	return ctx.emit("join", output.Result{
		Data: jsonx.Obj{"actor": res.Actor, "sessionId": res.Session.Str("id"), "resumed": res.Resumed},
		Text: text,
	})
}

func cmdWhoami(ctx *Ctx) (int, error) {
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
	mine := []store.Decorated{}
	for _, d := range store.ListClaims(ws, true) {
		if d.Claim.Str("sessionId") == session.Str("id") && !d.Stale {
			mine = append(mine, d)
		}
	}
	lines := []string{fmt.Sprintf("%s (%s)  session %s  holding %d card(s)",
		actor.Str("name"), actor.Str("vendor"), session.Str("id"), len(mine))}
	claims := jsonx.Arr{}
	for _, d := range mine {
		task := d.Claim.Str("task")
		if task == "" {
			task = "-"
		}
		lines = append(lines, fmt.Sprintf("  %s  %s  -> %s", d.Claim.Str("id"),
			strings.Join(d.Claim.Strings("scope.include"), ", "), task))
		claims = append(claims, jsonx.Obj{
			"id": d.Claim.Str("id"), "include": strArr(d.Claim.Strings("scope.include")),
			"task": d.Claim.Get("task"), "expiresAt": d.EffectiveExpiresAt,
		})
	}
	return ctx.emit("whoami", output.Result{
		Data: jsonx.Obj{"actor": actor, "sessionId": session.Str("id"), "host": actor.Str("host"), "claims": claims},
		Text: strings.Join(lines, "\n"),
	})
}

func strArr(in []string) jsonx.Arr {
	out := jsonx.Arr{}
	for _, s := range in {
		out = append(out, s)
	}
	return out
}

func cmdVersion(ctx *Ctx) (int, error) {
	data := jsonx.Obj{
		"product": brand.Product, "package": brand.Package, "version": brand.Version, "protocol": brand.Protocol,
		"implementation": "go", "runtime": runtime.Version(), "platform": nodePlatform(), "arch": nodeArch(),
	}
	return ctx.emit("version", output.Result{
		Data: data,
		Text: fmt.Sprintf("%s %s  protocol %s  go %s  %s/%s",
			brand.Package, brand.Version, brand.Protocol, runtime.Version(), nodePlatform(), nodeArch()),
	})
}

// nodePlatform reports the platform using Node's vocabulary so that a workspace
// reads the same whichever implementation wrote it.
func nodePlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "darwin":
		return "darwin"
	default:
		return runtime.GOOS
	}
}

func nodeArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func cmdConfig(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	key := ""
	if len(ctx.Positional) > 0 {
		key = ctx.Positional[0]
	}
	if key == "" {
		return ctx.emit("config", output.Result{Data: ws.Config, Text: jsonx.Indent(ws.Config, configOrder)})
	}
	current := ws.Config.Get(key)
	hasValue := len(ctx.Positional) > 1
	if !hasValue {
		if current == nil && !hasKey(ws.Config, key) {
			return 0, errs.New("E_NOT_FOUND", fmt.Sprintf("No config key '%s'.", key)).WithHint(brand.Bin + " config")
		}
		text := ""
		switch t := current.(type) {
		case jsonx.Obj, jsonx.Arr:
			text = jsonx.Compact(t)
		case string:
			text = t
		case bool:
			text = strconv.FormatBool(t)
		case float64:
			text = jsonx.FormatNumber(t)
		case nil:
			text = "null"
		}
		return ctx.emit("config", output.Result{Data: jsonx.Obj{"key": key, "value": current}, Text: text})
	}
	if current == nil && !hasKey(ws.Config, key) {
		return 0, errs.New("E_NOT_FOUND", fmt.Sprintf("No config key '%s'.", key)).WithHint(brand.Bin + " config")
	}
	value := ctx.Positional[1]
	var parsed any = value
	switch current.(type) {
	case float64:
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return 0, errs.New("E_USAGE", fmt.Sprintf("Config key '%s' needs a number.", key))
		}
		parsed = n
	case bool:
		if value != "true" && value != "false" {
			return 0, errs.New("E_USAGE", fmt.Sprintf("Config key '%s' needs true or false.", key))
		}
		parsed = value == "true"
	}
	if err := ws.Config.Set(key, parsed); err != nil {
		return 0, err
	}
	if err := fsx.WriteJSONAtomic(ws.Paths.Config, ws.Config, ws.Paths.Tmp); err != nil {
		return 0, err
	}
	return ctx.emit("config", output.Result{
		Data: jsonx.Obj{"key": key, "value": parsed, "previous": current},
		Text: fmt.Sprintf("%s = %s (was %s)", key, scalarText(parsed), scalarText(current)),
	})
}

// configOrder reproduces DEFAULT_CONFIG's declaration order so that the human
// readable `fridge config` dump is byte-identical to the Node one.
func configOrder(path string) []string {
	switch path {
	case "":
		return []string{"schema", "workspaceId", "lease", "mutex", "paths", "notes", "door", "git", "policy", "writer"}
	case "lease":
		return []string{"defaultTtlMs", "maxTtlMs", "renewOnAnyCommand", "renewThresholdRatio", "graceMs"}
	case "mutex":
		return []string{"acquireTimeoutMs", "staleMs", "maxHoldMs"}
	case "paths":
		return []string{"caseSensitivity", "unicodeNormalization", "strictExcludes", "materializeLimit", "allowGlobalClaims"}
	case "notes":
		return []string{"commit", "retainDays"}
	case "door":
		return []string{"path", "autoRender", "extraTargets"}
	case "git":
		return []string{"readOnly", "warnOnSyncedFolder"}
	case "policy":
		return []string{"requireTaskOnClaim", "requireClaimForWrite"}
	}
	return nil
}

func hasKey(o jsonx.Obj, dotted string) bool {
	parts := strings.Split(dotted, ".")
	var cur any = o
	for _, p := range parts {
		m, ok := cur.(jsonx.Obj)
		if !ok {
			return false
		}
		v, present := m[p]
		if !present {
			return false
		}
		cur = v
	}
	return true
}

func scalarText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return jsonx.FormatNumber(t)
	case nil:
		return "null"
	default:
		return jsonx.Compact(t)
	}
}

func cmdAdapters(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	sub := "install"
	if len(ctx.Positional) > 0 {
		sub = ctx.Positional[0]
	}
	if sub != "install" && sub != "check" && sub != "print" && sub != "list" {
		return 0, errs.New("E_USAGE", fmt.Sprintf("Unknown 'adapters %s'.", sub)).
			WithHint(brand.Bin + " adapters install|check|print|list")
	}
	if sub == "print" {
		return ctx.emit("adapters", output.Result{
			Data: jsonx.Obj{"hash": adapters.BodyHash(), "block": adapters.Block()},
			Text: adapters.Block(),
		})
	}
	if sub == "list" {
		rows := []adapters.Status{}
		lines := []string{}
		for _, k := range adapters.Order {
			st := adapters.StatusFor(ws.Root, k)
			rows = append(rows, st)
			lines = append(lines, padEnd(st.Vendor, 9)+padEnd(st.State, 10)+filepath.ToSlash(st.File))
		}
		return ctx.emit("adapters", output.Result{
			Data: jsonx.Obj{"vendors": adapterArr(rows)}, Text: strings.Join(lines, "\n"),
		})
	}
	keys := adapters.Order
	if v := ctx.Flags.Str("vendor"); v != "" {
		requested := []string{}
		wantAll := false
		for _, s := range strings.Split(v, ",") {
			s = strings.TrimSpace(s)
			requested = append(requested, s)
			if s == "all" {
				wantAll = true
			}
		}
		if !wantAll {
			keys = requested
		}
	}
	check := sub == "check" || ctx.Flags.Bool("check")
	results, err := adapters.Install(ws.Root, keys, check, ws.Paths.Tmp)
	if err != nil {
		return 0, err
	}
	bad := 0
	lines := []string{}
	for _, r := range results {
		head := "ok      "
		if r.State != "current" {
			head = padEnd(r.State, 8)
			bad++
		}
		suffix := ""
		if r.Action != "" && !check {
			suffix = "  (" + r.Action + ")"
		}
		lines = append(lines, head+filepath.ToSlash(r.File)+suffix)
	}
	text := strings.Join(lines, "\n")
	if check && bad > 0 {
		return 0, errs.New("E_DRIFT", fmt.Sprintf("%d instruction file(s) are missing or out of date.", bad)).
			WithHint(brand.Bin + " adapters install").
			WithDetails(jsonx.Obj{"report": text, "vendors": adapterArr(results)})
	}
	if text == "" {
		text = "nothing to do"
	}
	return ctx.emit("adapters", output.Result{
		Data: jsonx.Obj{"vendors": adapterArr(results), "hash": adapters.BodyHash()}, Text: text,
	})
}

var legacyTodo = "To-do.done.md"
var legacyUpdates = "shared-development-updates.md"

type legacyEntry struct {
	Heading string
	Body    string
	File    string
}

var headingRe = regexp.MustCompile(`^#{1,6}\s+`)
var bulletRe = regexp.MustCompile(`^\s*[-*]\s+`)
var stripHeadingRe = regexp.MustCompile(`^#+\s+`)
var stripBulletRe = regexp.MustCompile(`^\s*[-*]\s+`)
var newlineRe = regexp.MustCompile(`\r?\n`)

// parseLegacy is deliberately dumb: one note per bullet or heading, in file
// order. Guessing wrong about someone's history is worse than importing it
// verbatim.
func parseLegacy(text, file string) []legacyEntry {
	out := []legacyEntry{}
	lines := newlineRe.Split(text, -1)
	heading := ""
	buf := []string{}
	flush := func() {
		if len(buf) == 0 {
			return
		}
		body := strings.TrimSpace(strings.Join(buf, "\n"))
		if body != "" {
			out = append(out, legacyEntry{Heading: heading, Body: body, File: file})
		}
		buf = nil
	}
	for _, line := range lines {
		if headingRe.MatchString(line) {
			flush()
			heading = strings.TrimSpace(stripHeadingRe.ReplaceAllString(line, ""))
			continue
		}
		if bulletRe.MatchString(line) {
			flush()
			buf = append(buf, strings.TrimSpace(stripBulletRe.ReplaceAllString(line, "")))
			continue
		}
		buf = append(buf, line)
	}
	flush()
	return out
}

func parseAuthorMap(raw []string) (map[string]string, error) {
	m := map[string]string{}
	if len(raw) == 0 {
		return m, nil
	}
	for _, pair := range strings.Split(strings.Join(raw, ","), ",") {
		if strings.TrimSpace(pair) == "" {
			continue
		}
		i := strings.Index(pair, "=")
		if i == -1 {
			return nil, errs.New("E_USAGE",
				fmt.Sprintf("--author-map wants \"old=new\" pairs, got '%s'.", strings.TrimSpace(pair))).
				WithHint("--author-map \"agent1=claude,agent2=copilot\"")
		}
		m[strings.ToLower(strings.TrimSpace(pair[:i]))] = strings.TrimSpace(pair[i+1:])
	}
	return m, nil
}

var authorRe = regexp.MustCompile(`^\s*(?:\*\*|__|\[|@)?\s*([A-Za-z][A-Za-z0-9 ._-]{0,40}?)\s*(?:\*\*|__|\])?\s*[:>-]\s+`)

// attributeEntry is deliberately conservative. A leading "name:" is only
// believed when the name is already known, because guessing an author from
// prose would put words in somebody's mouth.
func attributeEntry(e legacyEntry, authorMap map[string]string, known map[string]jsonx.Obj, fallback jsonx.Obj) (jsonx.Obj, string) {
	candidate := ""
	if m := authorRe.FindStringSubmatch(e.Body); m != nil {
		candidate = strings.TrimSpace(m[1])
	} else {
		candidate = strings.TrimSpace(e.Heading)
	}
	if candidate == "" {
		return fallback, ""
	}
	key := strings.ToLower(candidate)
	if name, ok := authorMap[key]; ok {
		return jsonx.Obj{"id": nil, "name": name}, candidate
	}
	if a, ok := known[key]; ok {
		return jsonx.Obj{"id": a.Str("id"), "name": a.Str("name")}, candidate
	}
	return fallback, ""
}

func cmdMigrate(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	actor, session, err := store.RequireActor(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	authorMap, err := parseAuthorMap(ctx.Flags.List("author-map"))
	if err != nil {
		return 0, err
	}
	known := map[string]jsonx.Obj{}
	for _, a := range store.ListActors(ws) {
		known[strings.ToLower(a.Str("name"))] = a
		known[strings.ToLower(a.Str("slug"))] = a
	}
	type target struct{ rel, kind string }
	targets := []target{}
	todo := ctx.Flags.Str("todo-done")
	if todo == "" && fsx.Exists(filepath.Join(ws.Root, legacyTodo)) {
		todo = legacyTodo
	}
	updates := ctx.Flags.Str("updates")
	if updates == "" && fsx.Exists(filepath.Join(ws.Root, legacyUpdates)) {
		updates = legacyUpdates
	}
	if todo != "" {
		targets = append(targets, target{todo, "legacy.todo"})
	}
	if updates != "" {
		targets = append(targets, target{updates, "legacy.update"})
	}
	if len(targets) == 0 {
		return 0, errs.New("E_NOT_FOUND",
			fmt.Sprintf("No legacy files found (%s, %s).", legacyTodo, legacyUpdates)).
			WithHint(brand.Bin + " migrate --todo-done <file> --updates <file>")
	}
	dryRun := ctx.Flags.Bool("dry-run")
	imported := jsonx.Arr{}
	count := 0
	for _, t := range targets {
		abs := filepath.Join(ws.Root, filepath.FromSlash(t.rel))
		raw, ok := fsx.ReadTextOr(abs)
		if !ok {
			return 0, errs.New("E_NOT_FOUND", fmt.Sprintf("Cannot read %s: ENOENT", t.rel))
		}
		for _, e := range parseLegacy(raw, t.rel) {
			credited, detected := attributeEntry(e, authorMap, known, actor)
			firstLine := strings.SplitN(e.Body, "\n", 2)[0]
			if !dryRun {
				var det any
				if detected != "" {
					det = detected
				}
				var head any
				if e.Heading != "" {
					head = e.Heading
				}
				if _, err := store.Pin(ws, store.PinArgs{
					Type: t.kind, Actor: credited, Session: session,
					Subject: jsonx.Obj{"kind": "file", "id": t.rel},
					Summary: sliceStr(firstLine, 200),
					Data: jsonx.Obj{
						"sourceFile": t.rel, "heading": head, "body": e.Body,
						"importedAt": util.Now(), "importedBy": actor.Str("name"),
						"attributedTo": credited.Str("name"), "detectedAuthor": det,
					},
				}); err != nil {
					return 0, err
				}
			}
			var head any
			if e.Heading != "" {
				head = e.Heading
			}
			imported = append(imported, jsonx.Obj{
				"file": t.rel, "heading": head, "attributedTo": credited.Str("name"),
				"summary": sliceStr(firstLine, 120),
			})
			count++
		}
		if !dryRun && ctx.Flags.Bool("freeze") {
			banner := strings.Join([]string{
				"<!-- FROZEN by fridge migrate.",
				fmt.Sprintf("     Imported into %s/notes/ on %s by %s.", brand.StateDir, util.Now(), actor.Str("name")),
				fmt.Sprintf("     Do not edit this file. Pin notes with: %s pin \"...\"", brand.Bin),
				fmt.Sprintf("     Read history with: %s log -->", brand.Bin),
				"",
			}, "\n")
			if !strings.HasPrefix(raw, "<!-- FROZEN") {
				if err := fsx.WriteAtomic(abs, banner+raw, ws.Paths.Tmp); err != nil {
					return 0, err
				}
			}
		}
	}
	verb := "Imported"
	if dryRun {
		verb = "Would import"
	} else {
		render.Auto(ws)
	}
	rels := []string{}
	for _, t := range targets {
		rels = append(rels, t.rel)
	}
	lines := []string{fmt.Sprintf("%s %d entr(ies) from %s.", verb, count, strings.Join(rels, ", "))}
	if ctx.Flags.Bool("freeze") && !dryRun {
		lines = append(lines, "Legacy files marked FROZEN. They are now read-only history.")
	}
	if !dryRun {
		n := count
		if n > 50 {
			n = 50
		}
		lines = append(lines, fmt.Sprintf("Read them back with: %s log --limit %d", brand.Bin, n))
	}
	head := imported
	if len(head) > 200 {
		head = head[:200]
	}
	return ctx.emit("migrate", output.Result{
		Data: jsonx.Obj{"dryRun": dryRun, "count": float64(count), "entries": head, "files": strArr(rels)},
		Text: strings.Join(lines, "\n"),
	})
}

func sliceStr(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
