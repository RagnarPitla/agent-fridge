// SPDX-License-Identifier: Apache-2.0
// Workspace resolution and record IO. One writer per record, always.
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/fsx"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	pathutil "github.com/RagnarPitla/agent-fridge/internal/paths"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

// DefaultConfig mirrors DEFAULT_CONFIG in src/core/store.mjs byte for byte.
func DefaultConfig(workspaceID string) jsonx.Obj {
	return jsonx.Obj{
		"schema":      "wcp/0.1/config",
		"workspaceId": workspaceID,
		"lease": jsonx.Obj{
			"defaultTtlMs":        float64(900000),
			"maxTtlMs":            float64(14400000),
			"renewOnAnyCommand":   true,
			"renewThresholdRatio": 0.5,
			"graceMs":             float64(60000),
		},
		"mutex": jsonx.Obj{
			"acquireTimeoutMs": float64(10000),
			"staleMs":          float64(15000),
			"maxHoldMs":        float64(2000),
		},
		"paths": jsonx.Obj{
			"caseSensitivity":      "auto",
			"unicodeNormalization": "NFC",
			"strictExcludes":       false,
			"materializeLimit":     float64(5000),
			"allowGlobalClaims":    false,
		},
		"notes": jsonx.Obj{"commit": true, "retainDays": float64(0)},
		"door": jsonx.Obj{
			"path":         brand.StateDir + "/DOOR.md",
			"autoRender":   true,
			"extraTargets": jsonx.Arr{},
		},
		"git":    jsonx.Obj{"readOnly": true, "warnOnSyncedFolder": true},
		"policy": jsonx.Obj{"requireTaskOnClaim": true, "requireClaimForWrite": "advisory"},
		"writer": brand.Writer,
	}
}

// Paths are the well-known locations inside .fridge/.
type Paths struct {
	Dir        string
	Version    string
	Config     string
	Workspace  string
	Door       string
	Actors     string
	Sessions   string
	Claims     string
	Leases     string
	Notes      string
	Queue      string
	Inbox      string
	Locks      string
	Mutex      string
	Tmp        string
	Archive    string
	Quarantine string
	Views      string
}

// StatePaths resolves every state location under a repository root.
func StatePaths(root string) Paths {
	dir := filepath.Join(root, brand.StateDir)
	return Paths{
		Dir:        dir,
		Version:    filepath.Join(dir, "VERSION"),
		Config:     filepath.Join(dir, "config.json"),
		Workspace:  filepath.Join(dir, "workspace.json"),
		Door:       filepath.Join(dir, "DOOR.md"),
		Actors:     filepath.Join(dir, "actors"),
		Sessions:   filepath.Join(dir, "sessions"),
		Claims:     filepath.Join(dir, "claims"),
		Leases:     filepath.Join(dir, "leases"),
		Notes:      filepath.Join(dir, "notes"),
		Queue:      filepath.Join(dir, "queue"),
		Inbox:      filepath.Join(dir, "inbox"),
		Locks:      filepath.Join(dir, "locks"),
		Mutex:      filepath.Join(dir, "locks", "registry.lock.d"),
		Tmp:        filepath.Join(dir, "tmp"),
		Archive:    filepath.Join(dir, "archive", "claims"),
		Quarantine: filepath.Join(dir, "quarantine"),
		Views:      filepath.Join(dir, "views"),
	}
}

// Workspace is one opened .fridge/ plus the resolved actor and session.
type Workspace struct {
	Root        string
	Paths       Paths
	Initialized bool
	Config      jsonx.Obj
	Cwd         string
	Version     string
	Actor       jsonx.Obj
	Session     jsonx.Obj
	SessID      string
}

// MutexDir satisfies mutex.Env.
func (ws *Workspace) MutexDir() string { return ws.Paths.Mutex }

// TmpDir satisfies mutex.Env.
func (ws *Workspace) TmpDir() string { return ws.Paths.Tmp }

// AcquireTimeoutMs satisfies mutex.Env.
func (ws *Workspace) AcquireTimeoutMs() int { return ws.Config.Int("mutex.acquireTimeoutMs") }

// StaleMs satisfies mutex.Env.
func (ws *Workspace) StaleMs() int { return ws.Config.Int("mutex.staleMs") }

// MaxHoldMs satisfies mutex.Env.
func (ws *Workspace) MaxHoldMs() int { return ws.Config.Int("mutex.maxHoldMs") }

// PinSystemNote satisfies mutex.Recorder, so lock.broken and lock.slow reach
// the wall without every caller of mutex.With having to opt in. Best effort by
// design: the lock decision has already been made and must not be undone by a
// failure to write evidence about it.
func (ws *Workspace) PinSystemNote(noteType, subject, summary string, data jsonx.Obj) {
	_, _ = Pin(ws, PinArgs{Type: noteType, Subject: subject, Summary: summary, Data: data})
}

// SessionID satisfies mutex.Env.
func (ws *Workspace) SessionID() string { return ws.SessID }

// GraceMs is the extra time a fallen card keeps its slot.
func (ws *Workspace) GraceMs() int64 { return int64(ws.Config.Num("lease.graceMs")) }

// FindRoot walks upward looking for .fridge/, remembering the nearest .git/.
func FindRoot(start string) (root string, initialized bool) {
	dir, err := filepath.Abs(start)
	if err != nil {
		dir = start
	}
	gitRoot := ""
	for {
		if fsx.Exists(filepath.Join(dir, brand.StateDir)) {
			return dir, true
		}
		if gitRoot == "" && fsx.Exists(filepath.Join(dir, ".git")) {
			gitRoot = dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if gitRoot != "" {
		return gitRoot, false
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		abs = start
	}
	return abs, false
}

// OpenOptions control workspace resolution.
type OpenOptions struct {
	Repo        string
	Cwd         string
	RequireInit bool
}

// Open resolves and validates a workspace.
func Open(opts OpenOptions) (*Workspace, error) {
	cwd := opts.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	start := cwd
	if opts.Repo != "" {
		if filepath.IsAbs(opts.Repo) {
			start = filepath.Clean(opts.Repo)
		} else {
			start = filepath.Clean(filepath.Join(cwd, opts.Repo))
		}
	}
	root, initialized := FindRoot(start)
	p := StatePaths(root)
	if !initialized {
		if opts.RequireInit {
			return nil, errs.New("E_NOT_INITIALIZED",
				fmt.Sprintf("No %s/ found from %s upward.", brand.StateDir, start)).WithHint("fridge init")
		}
		return &Workspace{Root: root, Paths: p, Initialized: false, Config: DefaultConfig(util.NewID("wsp")), Cwd: cwd}, nil
	}
	versionRaw := ""
	if text, ok := fsx.ReadTextOr(p.Version); ok {
		versionRaw = strings.TrimSpace(text)
	}
	if versionRaw == "" {
		return nil, errs.New("E_STATE_CORRUPT",
			fmt.Sprintf("%s/VERSION is missing or empty.", brand.StateDir)).WithHint("fridge doctor --fix")
	}
	if versionRaw != brand.Protocol {
		return nil, errs.New("E_PROTOCOL_VERSION",
			fmt.Sprintf("Workspace speaks %s; this binary speaks %s.", versionRaw, brand.Protocol)).
			WithHint("Upgrade agent-fridge, or use a matching version.")
	}
	loaded, ok := fsx.ReadJSONSafe(p.Config)
	if !ok {
		return nil, errs.New("E_STATE_CORRUPT", "Unreadable config: "+p.Config).WithHint("fridge doctor --fix")
	}
	wsID := loaded.Str("workspaceId")
	if wsID == "" {
		wsID = util.NewID("wsp")
	}
	config := jsonx.DeepMerge(DefaultConfig(wsID), loaded)
	door, err := ValidateDoorConfig(root, config, "E_STATE_CORRUPT")
	if err != nil {
		return nil, err
	}
	p.Door = door
	return &Workspace{Root: root, Paths: p, Initialized: true, Config: config, Cwd: cwd, Version: versionRaw}, nil
}

// ValidateDoorConfig checks shape and containment before any configured view is
// written. shapeCode is E_STATE_CORRUPT for files on disk and E_USAGE for a
// rejected config command.
func ValidateDoorConfig(root string, config jsonx.Obj, shapeCode string) (string, error) {
	invalid := func(message string) (string, error) {
		e := errs.New(shapeCode, message)
		if shapeCode == "E_STATE_CORRUPT" {
			e = e.WithHint("Fix .fridge/config.json or run fridge doctor --fix.")
		}
		return "", e
	}
	door, ok := config.Get("door").(jsonx.Obj)
	if !ok {
		return invalid("Config key door must be an object.")
	}
	doorPath, ok := door["path"].(string)
	if !ok || strings.TrimSpace(doorPath) == "" {
		return invalid("Config key door.path must be a non-empty string.")
	}
	targets, ok := door["extraTargets"].(jsonx.Arr)
	if !ok {
		return invalid("Config key door.extraTargets must be an array of non-empty strings.")
	}
	for _, raw := range targets {
		target, ok := raw.(string)
		if !ok || strings.TrimSpace(target) == "" {
			return invalid("Config key door.extraTargets must be an array of non-empty strings.")
		}
		if _, err := pathutil.ResolveInsideWorkspace(root, target, "door.extraTargets entry"); err != nil {
			return "", err
		}
	}
	return pathutil.ResolveInsideWorkspace(root, doorPath, "door.path")
}

// GitignoreFor is the allowlist written into .fridge/.gitignore.
//
// notes.commit: false has to be honoured here, not just in documentation: the
// notes wall is only kept out of Git if the ignore file stops un-ignoring it.
// Callers rewrite this file whenever that setting changes.
func GitignoreFor(commitNotes bool) string {
	tail := " notes.commit is false, so the notes wall stays local too."
	notes := ""
	if commitNotes {
		tail = " The notes wall is shared history."
		notes = "!/notes/\n"
	}
	return "# Managed by Agent Fridge (" + brand.Protocol + ").\n" +
		"# Live coordination state is machine-local." + tail + "\n" +
		"/*\n" +
		"!/.gitignore\n" +
		"!/VERSION\n" +
		"!/config.json\n" +
		"!/workspace.json\n" +
		notes +
		"!/actors/\n"
}

// Gitignore is the default allowlist, for a workspace that commits its notes.
var Gitignore = GitignoreFor(true)

// WriteGitignore rewrites .fridge/.gitignore from the live config.
func WriteGitignore(ws *Workspace) error {
	commitNotes := true
	if v, ok := ws.Config.Get("notes.commit").(bool); ok {
		commitNotes = v
	}
	return fsx.WriteAtomic(filepath.Join(ws.Paths.Dir, ".gitignore"), GitignoreFor(commitNotes), ws.Paths.Tmp)
}

// Init creates a fresh .fridge/ tree.
func Init(root string, force bool) (*Workspace, string, error) {
	p := StatePaths(root)
	if fsx.Exists(p.Dir) && !force {
		return nil, "", errs.New("E_ALREADY_EXISTS",
			fmt.Sprintf("%s/ already exists in %s.", brand.StateDir, root)).
			WithHint("Use --force to re-write config and ignore rules.")
	}
	for _, d := range []string{p.Dir, p.Actors, p.Sessions, p.Claims, p.Leases, p.Notes, p.Queue, p.Inbox, p.Locks, p.Tmp, p.Archive, p.Quarantine, p.Views} {
		if err := fsx.EnsureDir(d); err != nil {
			return nil, "", err
		}
	}
	_ = os.Chmod(p.Sessions, 0o700)
	workspaceID := util.NewID("wsp")
	if err := fsx.WriteAtomic(p.Version, brand.Protocol+"\n", p.Tmp); err != nil {
		return nil, "", err
	}
	if err := fsx.WriteAtomic(filepath.Join(p.Dir, ".gitignore"), Gitignore, p.Tmp); err != nil {
		return nil, "", err
	}
	if err := fsx.WriteJSONAtomic(p.Workspace, jsonx.Obj{
		"schema":        "wcp/0.1/workspace",
		"workspaceId":   workspaceID,
		"createdAt":     util.Now(),
		"createdOnHost": util.HostID(),
		"writer":        brand.Writer,
	}, p.Tmp); err != nil {
		return nil, "", err
	}
	if !fsx.Exists(p.Config) || force {
		if err := fsx.WriteJSONAtomic(p.Config, DefaultConfig(workspaceID), p.Tmp); err != nil {
			return nil, "", err
		}
	}
	return &Workspace{Root: root, Paths: p, Initialized: true, Config: DefaultConfig(workspaceID), Cwd: root, Version: brand.Protocol}, workspaceID, nil
}

// EnsureGitAttributes appends the merge rules Git needs, once.
func EnsureGitAttributes(root string) bool {
	file := filepath.Join(root, ".gitattributes")
	lines := []string{
		brand.StateDir + "/notes/** -text -merge",
		brand.StateDir + "/DOOR.md  linguist-generated=true",
		brand.StateDir + "/views/** linguist-generated=true",
	}
	current, _ := fsx.ReadTextOr(file)
	missing := []string{}
	for _, l := range lines {
		key := strings.SplitN(l, " ", 2)[0]
		if !strings.Contains(current, key) {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return false
	}
	next := current
	if current != "" && !strings.HasSuffix(current, "\n") {
		next += "\n"
	}
	if current != "" {
		next += "\n"
	}
	next += "# " + brand.Product + "\n" + strings.Join(missing, "\n") + "\n"
	if os.WriteFile(file, []byte(next), 0o666) != nil {
		return false
	}
	return true
}

// ---------------------------------------------------------------- actors

// ActorFile is where one housemate's card lives.
func ActorFile(ws *Workspace, name string) string {
	return filepath.Join(ws.Paths.Actors, util.Slug(name)+".json")
}

// ListActors returns every readable actor record.
func ListActors(ws *Workspace) []jsonx.Obj {
	out := []jsonx.Obj{}
	for _, f := range fsx.ListJSON(ws.Paths.Actors) {
		if v, ok := fsx.ReadJSONSafe(f); ok {
			out = append(out, v)
		}
	}
	return out
}

// ReadActor loads one actor by display name.
func ReadActor(ws *Workspace, name string) jsonx.Obj {
	if v, ok := fsx.ReadJSONSafe(ActorFile(ws, name)); ok {
		return v
	}
	return nil
}

// ResolveActorName applies the flag, then the env var, then the sole actor.
func ResolveActorName(ws *Workspace, explicit string) (string, error) {
	return ResolveActorNameFor(ws, explicit, false)
}

// ResolveActorNameFor resolves the acting identity. When mutating is true the
// identity must be stated, never guessed.
func ResolveActorNameFor(ws *Workspace, explicit string, mutating bool) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := os.Getenv("FRIDGE_ACTOR"); v != "" {
		return v, nil
	}
	actors := ListActors(ws)
	if len(actors) == 0 {
		return "", errs.New("E_NO_SESSION", "Nobody has put their name on the door yet.").
			WithHint("fridge join --agent <your-name>")
	}
	names := make([]string, 0, len(actors))
	for _, a := range actors {
		names = append(names, a.Str("name"))
	}
	// Being the only name on the door is not proof of who is typing. Two
	// terminals share one checkout, so inheriting the sole actor silently
	// gives the second terminal the first one's claims, which is exactly the
	// failure this tool exists to prevent. Reads may guess; writes may not.
	if mutating {
		return "", errs.New("E_NO_SESSION", "This command changes the door, so it has to know who you are.").
			WithHint("Pass --agent <name>, or export FRIDGE_ACTOR=<name>. On the door: " + strings.Join(names, ", "))
	}
	if len(actors) == 1 {
		return actors[0].Str("name"), nil
	}
	return "", errs.New("E_NO_SESSION",
		fmt.Sprintf("More than one housemate is on this door (%s).", strings.Join(names, ", "))).
		WithHint("Pass --agent <name>, or export FRIDGE_ACTOR=<name>.")
}

// JoinResult is what Join hands back.
type JoinResult struct {
	Actor   jsonx.Obj
	Session jsonx.Obj
	Resumed bool
}

// JoinActor writes or refreshes an actor plus its session.
func JoinActor(ws *Workspace, name, vendor string) (JoinResult, error) {
	existing := ReadActor(ws, name)
	actorID := existing.Str("id")
	if actorID == "" {
		actorID = util.NewID("act")
	}
	sessionID := ""
	if existing != nil {
		if cur := existing.Str("currentSessionId"); cur != "" && fsx.Exists(SessionFile(ws, cur)) {
			sessionID = cur
		}
	}
	if sessionID == "" {
		sessionID = util.NewID("ses")
	}
	if vendor == "" {
		if existing != nil && existing.Str("vendor") != "" {
			vendor = existing.Str("vendor")
		} else {
			vendor = "other"
		}
	}
	createdAt := existing.Str("createdAt")
	if createdAt == "" {
		createdAt = util.Now()
	}
	user := os.Getenv("USER")
	if user == "" {
		user = os.Getenv("USERNAME")
	}
	if user == "" {
		user = "unknown"
	}
	actor := jsonx.Obj{
		"schema":           "wcp/0.1/actor",
		"id":               actorID,
		"name":             name,
		"slug":             util.Slug(name),
		"vendor":           vendor,
		"host":             util.HostID(),
		"user":             user,
		"createdAt":        createdAt,
		"lastSeenAt":       util.Now(),
		"currentSessionId": sessionID,
		"writer":           brand.Writer,
	}
	if err := fsx.WriteJSONAtomic(ActorFile(ws, name), actor, ws.Paths.Tmp); err != nil {
		return JoinResult{}, err
	}
	prior := ReadSession(ws, sessionID)
	startedAt := prior.Str("startedAt")
	if startedAt == "" {
		startedAt = util.Now()
	}
	var tokens any = jsonx.Obj{}
	if prior != nil {
		if t, ok := prior["tokens"]; ok && t != nil {
			tokens = t
		}
	}
	session := jsonx.Obj{
		"schema":    "wcp/0.1/session",
		"id":        sessionID,
		"actorId":   actorID,
		"actorName": name,
		"startedAt": startedAt,
		"updatedAt": util.Now(),
		"pid":       float64(os.Getpid()),
		"host":      util.HostID(),
		"seq":       prior.Num("seq"),
		"tokens":    tokens,
		"writer":    brand.Writer,
	}
	if err := WriteSession(ws, session); err != nil {
		return JoinResult{}, err
	}
	return JoinResult{Actor: actor, Session: session, Resumed: prior != nil}, nil
}

// ---------------------------------------------------------------- sessions

// SessionFile is the private per-session record path.
func SessionFile(ws *Workspace, id string) string {
	return filepath.Join(ws.Paths.Sessions, id+".json")
}

// ReadSession loads one session record, or nil.
func ReadSession(ws *Workspace, id string) jsonx.Obj {
	if id == "" {
		return nil
	}
	if v, ok := fsx.ReadJSONSafe(SessionFile(ws, id)); ok {
		return v
	}
	return nil
}

// WriteSession persists a session, stamping updatedAt.
func WriteSession(ws *Workspace, session jsonx.Obj) error {
	if err := fsx.EnsureDir(ws.Paths.Sessions); err != nil {
		return err
	}
	file := SessionFile(ws, session.Str("id"))
	rec := session.With(jsonx.Obj{"updatedAt": util.Now()})
	if err := fsx.WriteJSONAtomic(file, rec, ws.Paths.Tmp); err != nil {
		return err
	}
	_ = os.Chmod(file, 0o600)
	return nil
}

// RequireActor resolves a joined housemate for a read-only command.
func RequireActor(ws *Workspace, agent, vendor string) (jsonx.Obj, jsonx.Obj, error) {
	return RequireActorFor(ws, agent, vendor, false)
}

// RequireActorMutating is RequireActor for a command that changes the door.
func RequireActorMutating(ws *Workspace, agent, vendor string) (jsonx.Obj, jsonx.Obj, error) {
	return RequireActorFor(ws, agent, vendor, true)
}

// RequireActorFor resolves the actor and its live session. Actor and session
// creation belong only to join; a typo on --agent must not mutate the door.
func RequireActorFor(ws *Workspace, agent, vendor string, mutating bool) (jsonx.Obj, jsonx.Obj, error) {
	name, err := ResolveActorNameFor(ws, agent, mutating)
	if err != nil {
		return nil, nil, err
	}
	actor := ReadActor(ws, name)
	if actor == nil {
		return nil, nil, errs.New("E_NO_SESSION",
			fmt.Sprintf("No housemate named '%s' on this door.", name)).
			WithHint("fridge join --agent " + name)
	}
	session := ReadSession(ws, actor.Str("currentSessionId"))
	if session == nil {
		return nil, nil, errs.New("E_NO_SESSION",
			fmt.Sprintf("%s has no current session on this door.", actor.Str("name"))).
			WithHint(fmt.Sprintf("fridge join --agent %s --vendor %s", actor.Str("name"), actor.Str("vendor")))
	}
	ws.Actor, ws.Session, ws.SessID = actor, session, session.Str("id")
	return actor, session, nil
}

// RenewOwnLeases is piggyback renewal: any command you run is proof you are still
// alive, so it refreshes your own leases when they are more than half used up.
// No daemon needed.
func RenewOwnLeases(ws *Workspace, session jsonx.Obj) ([]string, error) {
	if session == nil || os.Getenv("FRIDGE_NO_RENEW") == "1" {
		return nil, nil
	}
	if !ws.Config.Bool("lease.renewOnAnyCommand") {
		return nil, nil
	}
	ratio := ws.Config.Num("lease.renewThresholdRatio")
	renewed := []string{}
	claims, err := ListClaimsStrict(ws, true)
	if err != nil {
		return nil, err
	}
	for _, d := range claims {
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
		if _, err := WriteLease(ws, c.Str("id"), session.Str("id"), ttl, int(d.Lease.Num("renewals"))+1); err != nil {
			return nil, err
		}
		renewed = append(renewed, c.Str("id"))
	}
	return renewed, nil
}

// MutateSession is a read-modify-write of one session record, always from what
// is on disk.
//
// A session object read before the mutex is a photograph, and writing it back
// afterwards silently reverts anything another process in the same session
// recorded meanwhile. Callers mutate the fresh copy this hands them.
func MutateSession(ws *Workspace, session jsonx.Obj, fn func(jsonx.Obj)) error {
	id := session.Str("id")
	if id == "" {
		return nil
	}
	fresh := ReadSession(ws, id)
	if fresh == nil {
		fresh = session.Clone()
	}
	fn(fresh)
	if err := WriteSession(ws, fresh); err != nil {
		return err
	}
	if v, ok := fresh.Get("tokens").(jsonx.Obj); ok {
		session["tokens"] = v
	}
	return nil
}

// ---------------------------------------------------------------- notes

// PinArgs is one note about to go on the wall.
type PinArgs struct {
	Type    string
	Actor   jsonx.Obj
	Session jsonx.Obj
	Subject any
	Summary string
	Data    jsonx.Obj
}

// Pin writes one immutable note. Filenames encode (ts, seq, slug) so two
// processes in the same millisecond cannot collide.
func Pin(ws *Workspace, args PinArgs) (jsonx.Obj, error) {
	ts := util.Now()
	t, _ := util.ParseISO(ts)
	t = t.UTC()
	dir := filepath.Join(ws.Paths.Notes,
		fmt.Sprintf("%d", t.Year()),
		fmt.Sprintf("%02d", int(t.Month())),
		fmt.Sprintf("%02d", t.Day()))
	seq := float64(0)
	if args.Session != nil {
		seq = args.Session.Num("seq") + 1
		args.Session["seq"] = seq
		_ = WriteSession(ws, args.Session)
	}
	actorName := "system"
	var actorID any
	if args.Actor != nil {
		if n := args.Actor.Str("name"); n != "" {
			actorName = n
		}
		if id := args.Actor.Str("id"); id != "" {
			actorID = id
		}
	}
	var sessionID any
	if args.Session != nil && args.Session.Str("id") != "" {
		sessionID = args.Session.Str("id")
	}
	data := args.Data
	if data == nil {
		data = jsonx.Obj{}
	}
	var subject any = args.Subject
	for attempt := 0; attempt < 5; attempt++ {
		id := util.NewID("evt")
		name := fmt.Sprintf("%s--%04d--%s--%s.json", util.CompactTs(ts), int(seq), util.Slug(actorName), id)
		note := jsonx.Obj{
			"schema":    "wcp/0.1/note",
			"id":        id,
			"type":      args.Type,
			"ts":        ts,
			"actorId":   actorID,
			"actorName": actorName,
			"sessionId": sessionID,
			"seq":       seq,
			"subject":   subject,
			"summary":   args.Summary,
			"data":      data,
			"writer":    brand.Writer,
		}
		err := fsx.CreateJSONExclusive(filepath.Join(dir, name), note, ws.Paths.Tmp)
		if err == nil {
			return note, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
	}
	return nil, errs.New("E_STATE_CORRUPT", "Could not pin a note: filename collision after 5 attempts.")
}

// NoteFilter narrows a notes query.
type NoteFilter struct {
	Limit int
	Since int64
	Until int64
	Actor string
	Type  string
}

// ReadNotes returns the most recent notes, oldest first.
func ReadNotes(ws *Workspace, f NoteFilter) []jsonx.Obj {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	files := fsx.WalkJSON(ws.Paths.Notes)
	out := []jsonx.Obj{}
	for i := len(files) - 1; i >= 0; i-- {
		n, ok := fsx.ReadJSONSafe(files[i])
		if !ok {
			continue
		}
		if f.Actor != "" && n.Str("actorName") != f.Actor {
			continue
		}
		if f.Type != "" && n.Str("type") != f.Type {
			continue
		}
		if f.Since != 0 || f.Until != 0 {
			ms, ok := util.ParseMs(n.Str("ts"))
			if !ok {
				continue
			}
			if f.Since != 0 && ms < f.Since {
				continue
			}
			if f.Until != 0 && ms > f.Until {
				continue
			}
		}
		out = append(out, n)
		if len(out) >= limit {
			break
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// CountNotes counts every note on the wall.
func CountNotes(ws *Workspace) int { return len(fsx.WalkJSON(ws.Paths.Notes)) }

// ---------------------------------------------------------------- claims and leases

// HeldStates: a card is still on the door while it is offered, so a handoff
// never leaves work unowned.
var HeldStates = map[string]bool{"active": true, "handoff-offered": true}

// IsHeld reports whether a claim still holds its slot.
func IsHeld(claim jsonx.Obj) bool { return HeldStates[claim.Str("state")] }

// ClaimFile is the path of one claim record.
func ClaimFile(ws *Workspace, id string) string {
	return filepath.Join(ws.Paths.Claims, id+".json")
}

// LeaseFile is the path of one lease record.
func LeaseFile(ws *Workspace, id string) string {
	return filepath.Join(ws.Paths.Leases, id+".json")
}

// ReadLease loads the lease for a claim, or nil.
func ReadLease(ws *Workspace, claimID string) jsonx.Obj {
	if v, ok := fsx.ReadJSONSafe(LeaseFile(ws, claimID)); ok {
		return v
	}
	return nil
}

// WriteLease stamps a fresh expiry on a claim.
func WriteLease(ws *Workspace, claimID, sessionID string, ttlMs int64, renewals int) (jsonx.Obj, error) {
	var sess any
	if sessionID != "" {
		sess = sessionID
	}
	lease := jsonx.Obj{
		"schema":    "wcp/0.1/lease",
		"claimId":   claimID,
		"sessionId": sess,
		"pid":       float64(os.Getpid()),
		"renewedAt": util.Now(),
		"expiresAt": util.NowISO(time.Now().Add(time.Duration(ttlMs) * time.Millisecond)),
		"renewals":  float64(renewals),
		"seq":       float64(renewals),
		"writer":    brand.Writer,
	}
	if err := fsx.WriteJSONAtomic(LeaseFile(ws, claimID), lease, ws.Paths.Tmp); err != nil {
		return nil, err
	}
	return lease, nil
}

// Decorated is a claim plus everything derived from its lease.
type Decorated struct {
	Claim              jsonx.Obj
	Lease              jsonx.Obj
	EffectiveExpiresAt string
	ExpiresInMs        int64
	Expired            bool
	Stale              bool
	OwnerAlive         *bool
}

// Decorate computes expiry and staleness for one claim.
func Decorate(ws *Workspace, claim jsonx.Obj) Decorated {
	lease := ReadLease(ws, claim.Str("id"))
	effective := claim.Str("expiresAtInitial")
	if lease != nil && lease.Str("claimId") == claim.Str("id") {
		effective = lease.Str("expiresAt")
	}
	expiresMs, _ := util.ParseMs(effective)
	grace := ws.GraceMs()
	ownerHere := claim.Str("host") == util.HostID()
	var ownerAlive *bool
	if ownerHere {
		alive := util.ProcessAlive(claim.Int("process.pid"))
		ownerAlive = &alive
	}
	now := util.NowMs()
	expired := now > expiresMs
	stale := expired && (now > expiresMs+grace || (ownerHere && ownerAlive != nil && !*ownerAlive))
	return Decorated{
		Claim:              claim,
		Lease:              lease,
		EffectiveExpiresAt: effective,
		ExpiresInMs:        expiresMs - now,
		Expired:            expired,
		Stale:              stale,
		OwnerAlive:         ownerAlive,
	}
}

// ListClaims returns every claim on the door, oldest first.
func ListClaims(ws *Workspace, includeStale bool) []Decorated {
	out, _ := listClaims(ws, includeStale)
	return out
}

// ListClaimsTolerant returns the readable claims plus the paths of any records
// that could not be parsed, so a view can show damage instead of an empty,
// falsely safe board.
func ListClaimsTolerant(ws *Workspace, includeStale bool) ([]Decorated, []string) {
	return listClaims(ws, includeStale)
}

// ListClaimsStrict refuses to answer when any record is unreadable. Ownership
// decisions must fail closed: an unparseable exclusive card must never look
// like free space.
func ListClaimsStrict(ws *Workspace, includeStale bool) ([]Decorated, error) {
	out, corrupt := listClaims(ws, includeStale)
	if len(corrupt) > 0 {
		return nil, errs.New("E_STATE_CORRUPT",
			fmt.Sprintf("%d claim record(s) cannot be read, so ownership is unknown.", len(corrupt))).
			WithHint("fridge doctor --fix   (moves damaged records to .fridge/quarantine/)").
			WithDetails(map[string]any{"corrupt": corrupt})
	}
	return out, nil
}

func listClaims(ws *Workspace, includeStale bool) ([]Decorated, []string) {
	out := []Decorated{}
	corrupt := []string{}
	for _, file := range fsx.ListJSON(ws.Paths.Claims) {
		v, ok := fsx.ReadJSONSafe(file)
		if !ok {
			rel, err := filepath.Rel(ws.Root, file)
			if err != nil {
				rel = file
			}
			corrupt = append(corrupt, filepath.ToSlash(rel))
			continue
		}
		d := Decorate(ws, v)
		if !includeStale && d.Stale {
			continue
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Claim.Str("createdAt") < out[j].Claim.Str("createdAt")
	})
	sort.Strings(corrupt)
	return out, corrupt
}

// ReadClaim loads and decorates one claim by id. A missing card is (nil, nil);
// a card that exists but cannot be parsed is an error, never "not there".
func ReadClaim(ws *Workspace, id string) (*Decorated, error) {
	file := ClaimFile(ws, id)
	if _, err := os.Stat(file); err != nil {
		return nil, nil
	}
	v, ok := fsx.ReadJSONSafe(file)
	if !ok {
		return nil, errs.New("E_STATE_CORRUPT", "Card "+id+" exists but cannot be read, so its ownership is unknown.").
			WithHint("fridge doctor --fix   (moves damaged records to .fridge/quarantine/)")
	}
	d := Decorate(ws, v)
	return &d, nil
}

// SaveClaim persists a claim record.
func SaveClaim(ws *Workspace, claim jsonx.Obj) error {
	return fsx.WriteJSONAtomic(ClaimFile(ws, claim.Str("id")), claim, ws.Paths.Tmp)
}

// ArchiveClaim moves a claim to archive/ and drops its lease and waiters.
func ArchiveClaim(ws *Workspace, claim jsonx.Obj, state string) (jsonx.Obj, error) {
	final := claim.With(jsonx.Obj{"state": state, "closedAt": util.Now()})
	if err := fsx.EnsureDir(ws.Paths.Archive); err != nil {
		return nil, err
	}
	if err := fsx.WriteJSONAtomic(filepath.Join(ws.Paths.Archive, claim.Str("id")+".json"), final, ws.Paths.Tmp); err != nil {
		return nil, err
	}
	fsx.UnlinkQuiet(ClaimFile(ws, claim.Str("id")))
	fsx.UnlinkQuiet(LeaseFile(ws, claim.Str("id")))
	ClearQueueFor(ws, claim.Str("id"))
	return final, nil
}

// ---------------------------------------------------------------- queue

// WriteQueueEntry records one advisory waiter.
func WriteQueueEntry(ws *Workspace, entry jsonx.Obj) (jsonx.Obj, error) {
	if err := fsx.EnsureDir(ws.Paths.Queue); err != nil {
		return nil, err
	}
	record := jsonx.Obj{
		"schema":    brand.Protocol + "/queue",
		"writer":    brand.Writer,
		"createdAt": util.Now(),
	}
	for k, v := range entry {
		record[k] = v
	}
	if err := fsx.WriteJSONAtomic(filepath.Join(ws.Paths.Queue, record.Str("id")+".json"), record, ws.Paths.Tmp); err != nil {
		return nil, err
	}
	return record, nil
}

// ListQueue returns waiters, optionally for one claim, oldest first.
func ListQueue(ws *Workspace, claimID string) []jsonx.Obj {
	out := []jsonx.Obj{}
	for _, f := range fsx.ListJSON(ws.Paths.Queue) {
		v, ok := fsx.ReadJSONSafe(f)
		if !ok {
			continue
		}
		if claimID != "" && v.Str("claimId") != claimID {
			continue
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Str("createdAt") < out[j].Str("createdAt")
	})
	return out
}

// RemoveQueueEntry deletes one waiter marker.
func RemoveQueueEntry(ws *Workspace, id string) {
	fsx.UnlinkQuiet(filepath.Join(ws.Paths.Queue, id+".json"))
}

// ClearQueueFor wakes and removes every waiter on a claim.
func ClearQueueFor(ws *Workspace, claimID string) []jsonx.Obj {
	woken := ListQueue(ws, claimID)
	for _, e := range woken {
		RemoveQueueEntry(ws, e.Str("id"))
	}
	return woken
}

// ReapStale sweeps fallen cards. Must be called while holding the mutex.
func ReapStale(ws *Workspace, actor, session jsonx.Obj, force bool) ([]jsonx.Obj, error) {
	reaped := []jsonx.Obj{}
	claims, err := ListClaimsStrict(ws, true)
	if err != nil {
		return nil, err
	}
	for _, d := range claims {
		if !d.Stale && !(force && d.Expired) {
			continue
		}
		if _, err := ArchiveClaim(ws, d.Claim, "expired"); err != nil {
			return nil, err
		}
		var alive any
		if d.OwnerAlive != nil {
			alive = *d.OwnerAlive
		}
		include := d.Claim.Strings("scope.include")
		if _, err := Pin(ws, PinArgs{
			Type: "claim.expired", Actor: actor, Session: session,
			Subject: jsonx.Obj{"kind": "claim", "id": d.Claim.Str("id")},
			Summary: fmt.Sprintf("expired %s's card on %s", d.Claim.Str("actorName"), strings.Join(include, ", ")),
			Data: jsonx.Obj{
				"ownerProcessAlive": alive,
				"expiredAt":         d.EffectiveExpiresAt,
				"owner":             d.Claim.Str("actorName"),
				"forced":            force && !d.Stale,
			},
		}); err != nil {
			return nil, err
		}
		reaped = append(reaped, d.Claim)
	}
	return reaped, nil
}

// ---------------------------------------------------------------- inbox

// InboxDir is one housemate's message folder.
func InboxDir(ws *Workspace, actorSlug string) string {
	return filepath.Join(ws.Paths.Inbox, actorSlug)
}

// WriteMessage delivers one handoff offer.
func WriteMessage(ws *Workspace, message jsonx.Obj) (jsonx.Obj, error) {
	dir := InboxDir(ws, util.Slug(message.Str("toName")))
	if err := fsx.EnsureDir(dir); err != nil {
		return nil, err
	}
	if err := fsx.WriteJSONAtomic(filepath.Join(dir, message.Str("id")+".json"), message, ws.Paths.Tmp); err != nil {
		return nil, err
	}
	return message, nil
}

// ListMessages returns one housemate's inbox.
func ListMessages(ws *Workspace, actorName string) []jsonx.Obj {
	out := []jsonx.Obj{}
	for _, f := range fsx.ListJSON(InboxDir(ws, util.Slug(actorName))) {
		if v, ok := fsx.ReadJSONSafe(f); ok {
			out = append(out, v)
		}
	}
	return out
}

// DeleteMessage removes one message.
func DeleteMessage(ws *Workspace, actorName, id string) bool {
	p := filepath.Join(InboxDir(ws, util.Slug(actorName)), id+".json")
	if !fsx.Exists(p) {
		return false
	}
	return os.Remove(p) == nil
}

// ArchiveMessage moves one message out of the inbox into a terminal state.
// The protocol gives a message a lifecycle (offered -> accepted, declined,
// withdrawn, expired); deleting it destroys the record of which one happened.
func ArchiveMessage(ws *Workspace, message jsonx.Obj, state string) (jsonx.Obj, error) {
	dir := filepath.Join(ws.Paths.Dir, "archive", "messages")
	if err := fsx.EnsureDir(dir); err != nil {
		return nil, err
	}
	final := message.With(jsonx.Obj{"state": state, "closedAt": util.Now()})
	if err := fsx.WriteJSONAtomic(filepath.Join(dir, message.Str("id")+".json"), final, ws.Paths.Tmp); err != nil {
		return nil, err
	}
	DeleteMessage(ws, message.Str("toName"), message.Str("id"))
	return final, nil
}

// FindMessage looks up one message by id.
func FindMessage(ws *Workspace, actorName, id string) jsonx.Obj {
	for _, m := range ListMessages(ws, actorName) {
		if m.Str("id") == id {
			return m
		}
	}
	return nil
}
