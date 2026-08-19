// SPDX-License-Identifier: Apache-2.0
// Handoffs, inbox, doctor, and the multi-process simulation.
package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/RagnarPitla/agent-fridge/internal/adapters"
	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/fsx"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/mutex"
	"github.com/RagnarPitla/agent-fridge/internal/output"
	"github.com/RagnarPitla/agent-fridge/internal/render"
	"github.com/RagnarPitla/agent-fridge/internal/store"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

func envActor() string { return os.Getenv("FRIDGE_ACTOR") }

func cmdHandoff(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	actor, session, err := store.RequireActor(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	id := ""
	if len(ctx.Positional) > 0 {
		id = ctx.Positional[0]
	}
	to := ctx.Flags.Str("to")
	if id == "" {
		return 0, errs.New("E_USAGE", "Which card?").
			WithHint(brand.Bin + " handoff <claim-id> --to <housemate> --note \"...\"")
	}
	if to == "" {
		return 0, errs.New("E_USAGE", "--to <housemate> is required.").
			WithHint(fmt.Sprintf("%s handoff %s --to claude-b --note \"half done\"", brand.Bin, id))
	}
	target := store.ReadActor(ws, to)
	if target == nil {
		names := []string{}
		for _, a := range store.ListActors(ws) {
			names = append(names, a.Str("name"))
		}
		known := strings.Join(names, ", ")
		if known == "" {
			known = "(nobody yet)"
		}
		return 0, errs.New("E_NOT_FOUND", fmt.Sprintf("Nobody named '%s' is on this door.", to)).
			WithHint("Known: " + known)
	}
	d := store.ReadClaim(ws, id)
	if d == nil {
		return 0, errs.New("E_NOT_FOUND", fmt.Sprintf("No card %s.", id)).WithHint(brand.Bin + " board")
	}
	if d.Claim.Str("sessionId") != session.Str("id") && !ctx.Flags.Bool("force") {
		return 0, errs.New("E_NOT_OWNER", fmt.Sprintf("Card %s belongs to %s, not you.", id, d.Claim.Str("actorName"))).
			WithHint("You can only hand off your own cards.")
	}
	var noteVal, reasonVal any
	if n := ctx.Flags.Str("note"); n != "" {
		noteVal = n
	}
	if r := ctx.Flags.Str("reason"); r != "" {
		reasonVal = r
	}
	message := jsonx.Obj{
		"schema": "wcp/0.1/message", "id": util.NewID("msg"), "kind": "handoff", "claimId": id,
		"fromName": actor.Str("name"), "fromSessionId": session.Str("id"), "toName": target.Str("name"),
		"note": noteVal, "reason": reasonVal, "createdAt": util.Now(),
		"scope": strArr(d.Claim.Strings("scope.include")), "task": d.Claim.Get("task"),
		"state": "offered", "writer": ws.Config.Str("writer"),
	}
	if err := mutex.With(ws, "handoff", func() error {
		if _, e := store.WriteMessage(ws, message); e != nil {
			return e
		}
		return store.SaveClaim(ws, d.Claim.With(jsonx.Obj{
			"state": "handoff-offered", "offeredTo": target.Str("name"),
			"offeredMessageId": message.Str("id"), "updatedAt": util.Now(),
		}))
	}, nil); err != nil {
		return 0, err
	}
	noteSuffix := ""
	if n := ctx.Flags.Str("note"); n != "" {
		noteSuffix = ": " + n
	}
	if _, err := store.Pin(ws, store.PinArgs{
		Type: "handoff.offered", Actor: actor, Session: session,
		Subject: jsonx.Obj{"kind": "claim", "id": id},
		Summary: fmt.Sprintf("%s offered %s to %s%s", actor.Str("name"),
			strings.Join(d.Claim.Strings("scope.include"), ", "), target.Str("name"), noteSuffix),
		Data: jsonx.Obj{"messageId": message.Str("id"), "to": target.Str("name"),
			"note": message.Get("note"), "reason": message.Get("reason")},
	}); err != nil {
		return 0, err
	}
	text := strings.Join([]string{
		fmt.Sprintf("Offered card %s to %s.", id, target.Str("name")),
		fmt.Sprintf("  message  %s", message.Str("id")),
		fmt.Sprintf("  scope    %s", strings.Join(d.Claim.Strings("scope.include"), ", ")),
		"",
		fmt.Sprintf("They accept with: %s accept %s --agent %s", brand.Bin, message.Str("id"), target.Str("name")),
		"The card stays yours until they accept, so nothing is ever unowned.",
	}, "\n")
	render.Auto(ws)
	return ctx.emit("handoff", output.Result{
		Data: jsonx.Obj{"messageId": message.Str("id"), "claimId": id, "to": target.Str("name")}, Text: text,
	})
}

func findOffer(ws *store.Workspace, actorName, key string) jsonx.Obj {
	if m := store.FindMessage(ws, actorName, key); m != nil {
		return m
	}
	for _, m := range store.ListMessages(ws, actorName) {
		if m.Str("claimId") == key {
			return m
		}
	}
	return nil
}

func cmdAccept(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	actor, session, err := store.RequireActor(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	if len(ctx.Positional) == 0 {
		return 0, errs.New("E_USAGE", "Accept which offer?").WithHint(brand.Bin + " inbox")
	}
	key := ctx.Positional[0]
	message := findOffer(ws, actor.Str("name"), key)
	if message == nil {
		return 0, errs.New("E_NOT_FOUND", fmt.Sprintf("No offer '%s' in your inbox.", key)).
			WithHint(brand.Bin + " inbox")
	}
	var result jsonx.Obj
	var inner error
	if err := mutex.With(ws, "accept", func() error {
		d := store.ReadClaim(ws, message.Str("claimId"))
		if d == nil {
			store.DeleteMessage(ws, actor.Str("name"), message.Str("id"))
			inner = errs.New("E_NOT_FOUND", fmt.Sprintf("Card %s is already gone.", message.Str("claimId"))).
				WithHint(brand.Bin + " claim ... to take the work fresh")
			return nil
		}
		token := util.RandomToken()
		history := d.Claim.ArrAt("handoffHistory")
		if history == nil {
			history = jsonx.Arr{}
		}
		history = append(history, jsonx.Obj{
			"from": message.Str("fromName"), "to": actor.Str("name"),
			"at": util.Now(), "note": message.Get("note"),
		})
		updated := d.Claim.With(jsonx.Obj{
			"actorId": actor.Str("id"), "actorName": actor.Str("name"), "vendor": actor.Str("vendor"),
			"sessionId": session.Str("id"), "host": util.HostID(),
			"process": jsonx.Obj{"pid": float64(os.Getpid()), "ppid": float64(os.Getppid()), "startedAt": util.Now()},
			"state":   "active", "offeredTo": nil, "offeredMessageId": nil,
			"tokenHash": util.SHA256(token), "handoffHistory": history, "updatedAt": util.Now(),
		})
		if e := store.SaveClaim(ws, updated); e != nil {
			return e
		}
		if _, e := store.WriteLease(ws, updated.Str("id"), session.Str("id"), int64(updated.Num("ttlMs")), 0); e != nil {
			return e
		}
		tokens := session.ObjAt("tokens")
		if tokens == nil {
			tokens = jsonx.Obj{}
		}
		tokens[updated.Str("id")] = token
		session["tokens"] = tokens
		if e := store.WriteSession(ws, session); e != nil {
			return e
		}
		store.DeleteMessage(ws, actor.Str("name"), message.Str("id"))
		result = updated
		return nil
	}, nil); err != nil {
		return 0, err
	}
	if inner != nil {
		return 0, inner
	}
	if _, err := store.Pin(ws, store.PinArgs{
		Type: "handoff.accepted", Actor: actor, Session: session,
		Subject: jsonx.Obj{"kind": "claim", "id": result.Str("id")},
		Summary: fmt.Sprintf("%s took over %s from %s", actor.Str("name"),
			strings.Join(result.Strings("scope.include"), ", "), message.Str("fromName")),
		Data: jsonx.Obj{"messageId": message.Str("id"), "from": message.Str("fromName"), "note": message.Get("note")},
	}); err != nil {
		return 0, err
	}
	lines := []string{
		fmt.Sprintf("Card %s is now yours (from %s).", result.Str("id"), message.Str("fromName")),
		fmt.Sprintf("  scope  %s", strings.Join(result.Strings("scope.include"), ", ")),
	}
	if n := message.Str("note"); n != "" {
		lines = append(lines, fmt.Sprintf("  note   %s", n))
	}
	lines = append(lines, "", fmt.Sprintf("When you stop: %s release %s --outcome done --note \"...\"", brand.Bin, result.Str("id")))
	render.Auto(ws)
	return ctx.emit("accept", output.Result{
		Data: jsonx.Obj{"claimId": result.Str("id"), "from": message.Str("fromName"),
			"scope": strArr(result.Strings("scope.include"))},
		Text: strings.Join(lines, "\n"),
	})
}

func cmdDecline(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	actor, session, err := store.RequireActor(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	if len(ctx.Positional) == 0 {
		return 0, errs.New("E_USAGE", "Decline which offer?").WithHint(brand.Bin + " inbox")
	}
	key := ctx.Positional[0]
	message := findOffer(ws, actor.Str("name"), key)
	if message == nil {
		return 0, errs.New("E_NOT_FOUND", fmt.Sprintf("No offer '%s' in your inbox.", key)).
			WithHint(brand.Bin + " inbox")
	}
	if err := mutex.With(ws, "decline", func() error {
		if d := store.ReadClaim(ws, message.Str("claimId")); d != nil {
			if e := store.SaveClaim(ws, d.Claim.With(jsonx.Obj{
				"state": "active", "offeredTo": nil, "offeredMessageId": nil, "updatedAt": util.Now(),
			})); e != nil {
				return e
			}
		}
		store.DeleteMessage(ws, actor.Str("name"), message.Str("id"))
		return nil
	}, nil); err != nil {
		return 0, err
	}
	reasonSuffix := ""
	var reasonVal any
	if r := ctx.Flags.Str("reason"); r != "" {
		reasonSuffix = ": " + r
		reasonVal = r
	}
	if _, err := store.Pin(ws, store.PinArgs{
		Type: "handoff.declined", Actor: actor, Session: session,
		Subject: jsonx.Obj{"kind": "claim", "id": message.Str("claimId")},
		Summary: fmt.Sprintf("%s declined %s's handoff%s", actor.Str("name"), message.Str("fromName"), reasonSuffix),
		Data:    jsonx.Obj{"messageId": message.Str("id"), "from": message.Str("fromName"), "reason": reasonVal},
	}); err != nil {
		return 0, err
	}
	render.Auto(ws)
	return ctx.emit("decline", output.Result{
		Data: jsonx.Obj{"messageId": message.Str("id"), "claimId": message.Str("claimId")},
		Text: fmt.Sprintf("declined %s; the card stays with %s", message.Str("id"), message.Str("fromName")),
	})
}

func cmdInbox(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	actor, _, err := store.RequireActor(ws, ctx.Flags.Str("agent"), ctx.Flags.Str("vendor"))
	if err != nil {
		return 0, err
	}
	messages := store.ListMessages(ws, actor.Str("name"))
	arr := jsonx.Arr{}
	blocks := []string{}
	for _, m := range messages {
		arr = append(arr, m)
		lines := []string{
			fmt.Sprintf("%s  %s  from %s", m.Str("id"), m.Str("kind"), m.Str("fromName")),
			fmt.Sprintf("  card   %s  %s", m.Str("claimId"), strings.Join(m.Strings("scope"), ", ")),
		}
		if t := m.Str("task"); t != "" {
			lines = append(lines, "  task   "+t)
		}
		if n := m.Str("note"); n != "" {
			lines = append(lines, "  note   "+n)
		}
		lines = append(lines, fmt.Sprintf("  accept: %s accept %s    decline: %s decline %s",
			brand.Bin, m.Str("id"), brand.Bin, m.Str("id")))
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	text := "nothing addressed to you"
	if len(blocks) > 0 {
		text = strings.Join(blocks, "\n\n")
	}
	return ctx.emit("inbox", output.Result{
		Data: jsonx.Obj{"actor": actor.Str("name"), "messages": arr}, Text: text,
	})
}

var syncHints = []string{"Dropbox", "OneDrive", "Google Drive", "iCloud Drive", "Creative Cloud Files"}

// DoctorPasses is the fixed-point budget: repairing one thing uncovers the next.
const DoctorPasses = 4

type finding struct {
	ID       string
	Severity string
	Message  string
	Fixable  bool
	Fixed    bool
	Hint     string
}

func (f finding) obj() jsonx.Obj {
	var hint any
	if f.Hint != "" {
		hint = f.Hint
	}
	return jsonx.Obj{"id": f.ID, "severity": f.Severity, "message": f.Message,
		"fixable": f.Fixable, "fixed": f.Fixed, "hint": hint}
}

func scanWorkspace(ws *store.Workspace) []finding {
	findings := []finding{}
	add := func(id, severity, message string, fixable bool, hint string) {
		findings = append(findings, finding{ID: id, Severity: severity, Message: message, Fixable: fixable, Hint: hint})
	}
	if !fsx.Exists(ws.Paths.Version) {
		add("version-missing", "error", brand.StateDir+"/VERSION is missing.", true, "")
	}
	if !fsx.Exists(filepath.Join(ws.Paths.Dir, ".gitignore")) {
		add("gitignore-missing", "warn", brand.StateDir+"/.gitignore is missing; live state could be committed.", true, "")
	}
	for _, file := range fsx.WalkJSON(ws.Paths.Dir) {
		if strings.HasPrefix(file, ws.Paths.Quarantine+string(filepath.Separator)) ||
			strings.HasPrefix(file, ws.Paths.Tmp+string(filepath.Separator)) {
			continue
		}
		if _, ok := fsx.ReadJSONSafe(file); !ok {
			rel, err := filepath.Rel(ws.Root, file)
			if err != nil {
				rel = file
			}
			add("corrupt:"+rel, "error", "Unreadable JSON: "+rel, true, "moved to .fridge/quarantine/ by --fix")
		}
	}
	claims := store.ListClaims(ws, true)
	stale := 0
	for _, d := range claims {
		if d.Stale {
			stale++
		}
	}
	if stale > 0 {
		add("stale-claims", "warn", fmt.Sprintf("%d card(s) have fallen off the door.", stale), true, brand.Bin+" reap")
	}
	for _, d := range claims {
		if d.Lease == nil {
			add("lease-missing:"+d.Claim.Str("id"), "warn",
				fmt.Sprintf("Card %s has no lease file.", d.Claim.Str("id")), true, "")
		}
		if d.Claim.Str("host") != util.HostID() {
			add("foreign-host:"+d.Claim.Str("id"), "info",
				fmt.Sprintf("Card %s was taken on another machine; liveness cannot be checked here.", d.Claim.Str("id")), false, "")
		}
	}
	for _, file := range fsx.ListJSON(ws.Paths.Leases) {
		v, ok := fsx.ReadJSONSafe(file)
		if !ok {
			continue
		}
		if !fsx.Exists(filepath.Join(ws.Paths.Claims, v.Str("claimId")+".json")) {
			add("orphan-lease:"+v.Str("claimId"), "warn",
				fmt.Sprintf("Lease for missing card %s.", v.Str("claimId")), true, "")
		}
	}
	tmpJunk := 0
	if entries, err := os.ReadDir(ws.Paths.Tmp); err == nil {
		for _, e := range entries {
			if info, err := e.Info(); err == nil {
				if util.NowMs()-info.ModTime().UnixMilli() > 3600000 {
					tmpJunk++
				}
			}
		}
	}
	if tmpJunk > 0 {
		add("tmp-junk", "info", fmt.Sprintf("%d stale temp file(s) from interrupted writes.", tmpJunk), true, "")
	}
	if fsx.Exists(ws.Paths.Mutex) {
		owner, ok := fsx.ReadJSONSafe(filepath.Join(ws.Paths.Mutex, "owner.json"))
		var acquired int64
		if ok {
			acquired, _ = util.ParseMs(owner.Str("acquiredAt"))
		}
		ageMs := util.NowMs() - acquired
		dead := ok && owner.Str("host") == util.HostID() && !util.ProcessAlive(owner.Int("pid"))
		if dead || ageMs > int64(ws.Config.Num("mutex.staleMs")) {
			why := fmt.Sprintf(" for %s", util.HumanMs(ageMs))
			if dead {
				why = " by a dead process"
			}
			add("mutex-held", "warn", fmt.Sprintf("The registry lock is held%s.", why), true, "")
		}
	}
	doorOnDisk, _ := fsx.ReadTextOr(ws.Paths.Door)
	if drift, _, _ := render.Drift(ws, doorOnDisk); drift {
		add("door-drift", "info", "DOOR.md is out of date.", true, brand.Bin+" render")
	}
	for _, key := range adapters.Order {
		st := adapters.StatusFor(ws.Root, key)
		if st.State == "drifted" {
			add("adapter-drift:"+key, "warn", filepath.ToSlash(st.File)+" has an out-of-date instruction block.",
				true, brand.Bin+" adapters install")
		}
	}
	for _, hint := range syncHints {
		if strings.Contains(ws.Root, hint) {
			add("cloud-sync", "warn", fmt.Sprintf(
				"This repository lives under %s. File sync can duplicate or delay %s/ writes; coordination guarantees are weaker.",
				hint, brand.StateDir), false, "")
		}
	}
	if info, err := os.Lstat(ws.Paths.Dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		add("state-symlink", "warn", brand.StateDir+" is a symlink. Only do this if you understand where it points.", false, "")
	}
	return findings
}

func applyFix(ws *store.Workspace, f *finding) error {
	switch {
	case f.ID == "version-missing":
		if err := fsx.WriteAtomic(ws.Paths.Version, brand.Protocol+"\n", ws.Paths.Tmp); err != nil {
			return err
		}
	case f.ID == "gitignore-missing":
		if err := fsx.WriteAtomic(filepath.Join(ws.Paths.Dir, ".gitignore"), store.Gitignore, ws.Paths.Tmp); err != nil {
			return err
		}
	case f.ID == "stale-claims":
		if err := mutex.With(ws, "doctor", func() error {
			_, e := store.ReapStale(ws, nil, nil, false)
			return e
		}, nil); err != nil {
			return err
		}
	case strings.HasPrefix(f.ID, "lease-missing:"):
		if d := store.ReadClaim(ws, strings.SplitN(f.ID, ":", 2)[1]); d != nil {
			if _, err := store.WriteLease(ws, d.Claim.Str("id"), d.Claim.Str("sessionId"),
				int64(d.Claim.Num("ttlMs")), 0); err != nil {
				return err
			}
		}
	case strings.HasPrefix(f.ID, "orphan-lease:"):
		fsx.UnlinkQuiet(filepath.Join(ws.Paths.Leases, strings.SplitN(f.ID, ":", 2)[1]+".json"))
	case f.ID == "tmp-junk":
		fsx.RmRF(ws.Paths.Tmp)
		if err := fsx.EnsureDir(ws.Paths.Tmp); err != nil {
			return err
		}
	case f.ID == "mutex-held":
		fsx.RmRF(ws.Paths.Mutex)
	case f.ID == "door-drift":
		if err := fsx.WriteAtomic(ws.Paths.Door, render.Door(ws), ws.Paths.Tmp); err != nil {
			return err
		}
	case strings.HasPrefix(f.ID, "adapter-drift:"):
		if _, err := adapters.Install(ws.Root, []string{strings.SplitN(f.ID, ":", 2)[1]}, false, ws.Paths.Tmp); err != nil {
			return err
		}
	case strings.HasPrefix(f.ID, "corrupt:"):
		rel := f.ID[len("corrupt:"):]
		from := filepath.Join(ws.Root, rel)
		to := filepath.Join(ws.Paths.Quarantine,
			fmt.Sprintf("%d--%s", util.NowMs(), strings.Join(strings.Split(rel, string(filepath.Separator)), "__")))
		if err := fsx.EnsureDir(ws.Paths.Quarantine); err != nil {
			return err
		}
		_ = os.Rename(from, to)
	}
	f.Fixed = true
	return nil
}

func cmdDoctor(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	fix := ctx.Flags.Bool("fix")
	findings := scanWorkspace(ws)
	repaired := []finding{}
	// Repairing one thing can uncover the next (quarantining a card orphans its
	// lease, every fix re-dirties the door), so fix to a fixed point.
	if fix {
		for pass := 0; pass < DoctorPasses; pass++ {
			fixable := []int{}
			for i, f := range findings {
				if f.Fixable {
					fixable = append(fixable, i)
				}
			}
			if len(fixable) == 0 {
				break
			}
			for _, i := range fixable {
				if err := applyFix(ws, &findings[i]); err != nil {
					return 0, err
				}
				repaired = append(repaired, findings[i])
			}
			findings = scanWorkspace(ws)
		}
	}
	order := []string{}
	byID := map[string]finding{}
	for _, f := range repaired {
		if _, ok := byID[f.ID]; !ok {
			order = append(order, f.ID)
		}
		byID[f.ID] = f
	}
	// A finding that survived its fix reports as unfixed.
	for _, f := range findings {
		if _, ok := byID[f.ID]; !ok {
			order = append(order, f.ID)
		}
		byID[f.ID] = f
	}
	report := jsonx.Arr{}
	lines := []string{}
	for _, id := range order {
		f := byID[id]
		report = append(report, f.obj())
		head := padEnd(strings.ToUpper(f.Severity), 5)
		if f.Fixed {
			head = "FIXED"
		}
		hint := ""
		if f.Hint != "" && !f.Fixed {
			hint = "  (" + f.Hint + ")"
		}
		lines = append(lines, head+"  "+f.Message+hint)
	}
	outstanding := 0
	for _, f := range findings {
		if f.Severity == "error" || f.Fixable {
			outstanding++
		}
	}
	text := "The door is tidy. Nothing to fix."
	if len(lines) > 0 {
		text = strings.Join(lines, "\n")
	}
	if ctx.Flags.Bool("check") && outstanding > 0 {
		return 0, errs.New("E_DRIFT", fmt.Sprintf("%d finding(s) need attention.", outstanding)).
			WithHint(brand.Bin + " doctor --fix").
			WithDetails(jsonx.Obj{"report": text, "findings": report})
	}
	return ctx.emit("doctor", output.Result{
		Data: jsonx.Obj{"findings": report, "fixed": float64(len(repaired)), "outstanding": float64(outstanding)},
		Text: text,
	})
}

type simResult struct {
	Index  int
	Code   int
	Stats  jsonx.Obj
	Stderr string
}

func cmdSimulate(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	agents := 6
	if v := ctx.Flags.Str("agents"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			agents = n
		}
	}
	if agents < 2 {
		agents = 2
	}
	durationMs := 8000
	if v := ctx.Flags.Str("duration"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			// A bare number is milliseconds, which is what the Node CLI accepts.
			durationMs = n
		} else if ms, err := util.ParseDuration(v, "duration"); err == nil {
			// The README advertises '--duration 60s'. The Node CLI runs that
			// through Number(), which yields NaN and quietly ends the run before
			// it starts; honouring the documented form is the intent.
			durationMs = int(ms)
		}
	}
	if durationMs < 1000 {
		durationMs = 1000
	}
	seed := 1234
	if v := ctx.Flags.Str("seed"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			seed = n
		}
	}
	self, err := os.Executable()
	if err != nil {
		return 0, errs.Internal(err)
	}
	startedAt := util.NowMs()
	results := make([]simResult, agents)
	var wg sync.WaitGroup
	for i := 0; i < agents; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = runSimWorker(self, ws.Root, i, seed+i, durationMs)
		}(i)
	}
	wg.Wait()
	elapsedMs := util.NowMs() - startedAt

	notes := []jsonx.Obj{}
	for _, f := range fsx.WalkJSON(ws.Paths.Notes) {
		if v, ok := fsx.ReadJSONSafe(f); ok {
			notes = append(notes, v)
		}
	}
	acquired, released, denied, pinned := []jsonx.Obj{}, []jsonx.Obj{}, []jsonx.Obj{}, []jsonx.Obj{}
	for _, n := range notes {
		switch t := n.Str("type"); {
		case t == "claim.acquired":
			acquired = append(acquired, n)
		case t == "claim.released" || t == "claim.expired":
			released = append(released, n)
		case t == "claim.denied":
			denied = append(denied, n)
		case strings.HasPrefix(t, "note."):
			pinned = append(pinned, n)
		}
	}
	expectedPins := 0
	allZero := true
	crashDetail := []string{}
	for _, r := range results {
		expectedPins += int(r.Stats.Num("pins"))
		if r.Code != 0 {
			allZero = false
			crashDetail = append(crashDetail, fmt.Sprintf("worker %d exited %d: %s", r.Index, r.Code, r.Stderr))
		}
	}
	crashText := "all workers exited 0"
	if len(crashDetail) > 0 {
		crashText = strings.Join(crashDetail, " | ")
	}
	readable := true
	for _, f := range fsx.WalkJSON(ws.Paths.Dir) {
		if _, ok := fsx.ReadJSONSafe(f); !ok {
			readable = false
		}
	}
	type inv struct {
		ID     string
		OK     bool
		Detail string
	}
	invariants := []inv{
		{"I1-no-lost-notes", len(pinned) >= expectedPins,
			fmt.Sprintf("%d pinned notes on disk, workers reported %d", len(pinned), expectedPins)},
		{"I2-no-double-ownership", true, ""},
		{"I3-no-crash", allZero, crashText},
		{"I5-state-readable", readable, "every JSON record parses"},
	}

	byScope := map[string][]span{}
	scopeOrder := []string{}
	for _, n := range acquired {
		for _, p := range n.Strings("data.include") {
			if _, ok := byScope[p]; !ok {
				scopeOrder = append(scopeOrder, p)
			}
			ts, _ := util.ParseMs(n.Str("ts"))
			byScope[p] = append(byScope[p], span{start: ts, actor: n.Str("actorName"), claimID: n.Str("subject.id")})
		}
	}
	closeAt := map[string]int64{}
	for _, n := range released {
		if id := n.Str("subject.id"); id != "" {
			ts, _ := util.ParseMs(n.Str("ts"))
			closeAt[id] = ts
		}
	}
	overlaps := 0
	for _, scope := range scopeOrder {
		list := append([]span{}, byScope[scope]...)
		for i := range list {
			if end, ok := closeAt[list[i].claimID]; ok {
				list[i].end = end
			} else {
				list[i].end = util.NowMs()
			}
		}
		sortSpans(list)
		for i := 1; i < len(list); i++ {
			if list[i].start < list[i-1].end && list[i].actor != list[i-1].actor {
				overlaps++
			}
		}
	}
	invariants[1].OK = overlaps == 0
	invariants[1].Detail = "no scope was exclusively held by two actors at once"
	if overlaps > 0 {
		invariants[1].Detail = fmt.Sprintf("%d overlapping exclusive holds on the same scope", overlaps)
	}

	ok := true
	for _, i := range invariants {
		if !i.OK {
			ok = false
		}
	}
	reportLines := []string{
		"# Agent Fridge concurrency simulation",
		"",
		fmt.Sprintf("- agents: %d", agents),
		fmt.Sprintf("- duration: %s", util.HumanMs(elapsedMs)),
		fmt.Sprintf("- seed: %d", seed),
		fmt.Sprintf("- node: %s on %s/%s", runtime.Version(), nodePlatform(), nodeArch()),
		"",
		"## Traffic",
		"",
		"| metric | count |",
		"|---|---|",
		fmt.Sprintf("| claims acquired | %d |", len(acquired)),
		fmt.Sprintf("| claims denied (conflict, exit 10) | %d |", len(denied)),
		fmt.Sprintf("| claims released or expired | %d |", len(released)),
		fmt.Sprintf("| notes pinned | %d |", len(pinned)),
		fmt.Sprintf("| total note files | %d |", len(notes)),
		"",
		"## Invariants",
		"",
		"| invariant | result | detail |",
		"|---|---|---|",
	}
	invArr := jsonx.Arr{}
	for _, i := range invariants {
		verdict := "FAIL"
		if i.OK {
			verdict = "PASS"
		}
		reportLines = append(reportLines, fmt.Sprintf("| %s | %s | %s |", i.ID, verdict, i.Detail))
		invArr = append(invArr, jsonx.Obj{"id": i.ID, "ok": i.OK, "detail": i.Detail})
	}
	finalVerdict := "FAIL"
	if ok {
		finalVerdict = "PASS"
	}
	reportLines = append(reportLines, "", "Result: "+finalVerdict, "")
	report := strings.Join(reportLines, "\n")
	if r := ctx.Flags.Str("report"); r != "" {
		if err := fsx.WriteAtomic(resolveIn(ws.Root, r), report, ws.Paths.Tmp); err != nil {
			return 0, err
		}
	}
	if !ok {
		return 0, errs.New("E_INTERNAL", "Simulation violated an invariant.").
			WithDetails(jsonx.Obj{"report": report, "invariants": invArr})
	}
	return ctx.emit("simulate", output.Result{
		Data: jsonx.Obj{
			"agents": float64(agents), "elapsedMs": float64(elapsedMs), "seed": float64(seed),
			"counts": jsonx.Obj{"acquired": float64(len(acquired)), "denied": float64(len(denied)),
				"released": float64(len(released)), "pinned": float64(len(pinned))},
			"invariants": invArr, "ok": ok,
		},
		Text: report,
	})
}

type span struct {
	start   int64
	actor   string
	claimID string
	end     int64
}

func sortSpans(list []span) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].start < list[j-1].start; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}
