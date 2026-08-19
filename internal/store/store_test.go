// SPDX-License-Identifier: Apache-2.0
// Unit tests for the record layer: actor resolution order, note filenames that
// cannot collide, and the staleness rules that decide when a card falls off.
package store

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/fsx"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

// scratchDir keeps fixtures inside the repo; the project never writes to the
// system temp directory.
func scratchDir(t *testing.T, label string) string {
	t.Helper()
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Dir(filepath.Dir(here))
	dir := filepath.Join(repo, ".scratch", "gotest", "store", label)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func newWorkspace(t *testing.T, label string) *Workspace {
	t.Helper()
	root := scratchDir(t, label)
	ws, _, err := Init(root, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	return ws
}

func TestInitLaysOutTheWholeTree(t *testing.T) {
	ws := newWorkspace(t, "init")
	for _, dir := range []string{ws.Paths.Claims, ws.Paths.Leases, ws.Paths.Notes, ws.Paths.Actors,
		ws.Paths.Sessions, ws.Paths.Queue, ws.Paths.Inbox, ws.Paths.Tmp, ws.Paths.Archive, ws.Paths.Views} {
		if !fsx.Exists(dir) {
			t.Errorf("missing directory %s", dir)
		}
	}
	if got := strings.TrimSpace(readText(t, ws.Paths.Version)); got == "" {
		t.Error("VERSION is empty")
	}
	if !strings.Contains(readText(t, filepath.Join(ws.Paths.Dir, ".gitignore")), "!/notes/") {
		t.Error(".fridge/.gitignore does not keep the notes wall")
	}
	if _, _, err := Init(ws.Root, false); code(err) != "E_ALREADY_EXISTS" {
		t.Errorf("second init returned %v, want E_ALREADY_EXISTS", err)
	}
	if _, _, err := Init(ws.Root, true); err != nil {
		t.Errorf("init --force failed: %v", err)
	}
}

func TestActorResolutionOrder(t *testing.T) {
	ws := newWorkspace(t, "actors")
	t.Setenv("FRIDGE_ACTOR", "")
	if _, err := ResolveActorName(ws, ""); code(err) != "E_NO_SESSION" {
		t.Errorf("empty door resolved to %v, want E_NO_SESSION", err)
	}
	if _, err := JoinActor(ws, "alice", "claude"); err != nil {
		t.Fatal(err)
	}
	// One housemate on the door is unambiguous.
	if name, err := ResolveActorName(ws, ""); err != nil || name != "alice" {
		t.Errorf("sole actor resolved to %q, %v", name, err)
	}
	if _, err := JoinActor(ws, "bob", "codex"); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveActorName(ws, ""); code(err) != "E_NO_SESSION" {
		t.Errorf("two actors resolved without a hint, want E_NO_SESSION, got %v", err)
	}
	t.Setenv("FRIDGE_ACTOR", "bob")
	if name, _ := ResolveActorName(ws, ""); name != "bob" {
		t.Errorf("FRIDGE_ACTOR ignored, got %q", name)
	}
	// The explicit flag outranks the environment.
	if name, _ := ResolveActorName(ws, "alice"); name != "alice" {
		t.Errorf("--agent did not win over FRIDGE_ACTOR, got %q", name)
	}
	// Actors are keyed by slug, so display case does not fork the identity.
	if name, _ := ResolveActorName(ws, "Alice"); util.Slug(name) != "alice" {
		t.Errorf("slug keying broken for %q", name)
	}
	if ReadActor(ws, "Alice").Str("id") != ReadActor(ws, "alice").Str("id") {
		t.Error("Alice and alice are different housemates")
	}
	if n := len(ListActors(ws)); n != 2 {
		t.Errorf("%d actors on the door, want 2", n)
	}
}

func TestJoinIsIdempotentAndResumesTheSession(t *testing.T) {
	ws := newWorkspace(t, "join")
	first, err := JoinActor(ws, "alice", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if first.Resumed {
		t.Error("first join resumed a session that did not exist")
	}
	if _, err := Pin(ws, PinArgs{Type: "note", Actor: first.Actor, Session: first.Session, Summary: "before"}); err != nil {
		t.Fatal(err)
	}
	second, err := JoinActor(ws, "alice", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Resumed {
		t.Error("second join did not resume the existing session")
	}
	if second.Actor.Str("id") != first.Actor.Str("id") {
		t.Error("re-joining minted a new actor id")
	}
	// Re-joining picks the session back up rather than starting a new one, so
	// the note sequence keeps counting instead of resetting to zero.
	if second.Session.Str("id") != first.Session.Str("id") {
		t.Error("re-joining abandoned the live session")
	}
	if second.Session.Str("startedAt") != first.Session.Str("startedAt") {
		t.Error("re-joining reset startedAt")
	}
	if second.Session.Num("seq") != 1 {
		t.Errorf("resumed seq is %v, want 1", second.Session.Num("seq"))
	}
	if ReadActor(ws, "alice").Str("currentSessionId") != second.Session.Str("id") {
		t.Error("the actor points at the wrong session")
	}
	// Losing the session file is survivable: the next join mints a fresh one.
	if err := os.Remove(SessionFile(ws, first.Session.Str("id"))); err != nil {
		t.Fatal(err)
	}
	third, err := JoinActor(ws, "alice", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if third.Resumed || third.Session.Str("id") == first.Session.Str("id") {
		t.Error("a lost session was not replaced")
	}
	if third.Actor.Str("id") != first.Actor.Str("id") {
		t.Error("a lost session changed the actor identity")
	}
}

var noteName = regexp.MustCompile(`^\d{8}T\d{6}\d*Z--\d{4}--[a-z0-9-]+--evt_[0-9A-Z]{26}\.json$`)

func TestNoteFilenamesCannotCollide(t *testing.T) {
	ws := newWorkspace(t, "notes")
	res, err := JoinActor(ws, "alice", "other")
	if err != nil {
		t.Fatal(err)
	}
	// Same actor, same session, as fast as the machine will go: the sequence
	// number is what keeps two notes in one millisecond apart.
	const count = 60
	for i := 0; i < count; i++ {
		if _, err := Pin(ws, PinArgs{Type: "note", Actor: res.Actor, Session: res.Session, Summary: "line"}); err != nil {
			t.Fatalf("pin %d: %v", i, err)
		}
	}
	files := fsx.WalkJSON(ws.Paths.Notes)
	if len(files) != count {
		t.Fatalf("%d note files on disk, want %d", len(files), count)
	}
	seen := map[string]bool{}
	sameMs := 0
	byMs := map[string]int{}
	for _, f := range files {
		base := filepath.Base(f)
		if !noteName.MatchString(base) {
			t.Errorf("note filename %q does not encode (ts, seq, slug, id)", base)
		}
		if seen[base] {
			t.Errorf("duplicate note filename %q", base)
		}
		seen[base] = true
		ms := strings.SplitN(base, "--", 2)[0]
		byMs[ms]++
		if byMs[ms] > 1 {
			sameMs++
		}
	}
	if sameMs == 0 {
		t.Log("no two notes landed in the same millisecond on this machine; the sequence number was untested")
	}
	// Notes are write-once: reading them back must return every line, newest
	// first, with the sequence strictly increasing over time.
	got := ReadNotes(ws, NoteFilter{Limit: count})
	if len(got) != count {
		t.Fatalf("read back %d notes, want %d", len(got), count)
	}
	seqs := []float64{}
	for _, n := range got {
		seqs = append(seqs, n.Num("seq"))
	}
	sorted := append([]float64{}, seqs...)
	sort.Float64s(sorted)
	for i := range sorted {
		if sorted[i] != float64(i+1) {
			t.Fatalf("sequence numbers are not 1..N: %v", sorted)
			break
		}
	}
}

func TestNotesAreWriteOnce(t *testing.T) {
	ws := newWorkspace(t, "write-once")
	res, _ := JoinActor(ws, "alice", "other")
	note, err := Pin(ws, PinArgs{Type: "note", Actor: res.Actor, Session: res.Session, Summary: "first"})
	if err != nil {
		t.Fatal(err)
	}
	files := fsx.WalkJSON(ws.Paths.Notes)
	before := readText(t, files[0])
	if _, err := Pin(ws, PinArgs{Type: "note", Actor: res.Actor, Session: res.Session, Summary: "second"}); err != nil {
		t.Fatal(err)
	}
	if after := readText(t, files[0]); after != before {
		t.Error("pinning a second note rewrote the first")
	}
	if note.Str("id") == "" || !strings.HasPrefix(note.Str("id"), "evt_") {
		t.Errorf("note id %q is not an event id", note.Str("id"))
	}
}

func TestStalenessFollowsTheLeaseNotThePid(t *testing.T) {
	ws := newWorkspace(t, "staleness")
	grace := ws.GraceMs()
	if grace <= 0 {
		t.Fatalf("grace is %d", grace)
	}
	now := util.NowMs()
	// A pid that is certainly not running, which is the normal case: agent CLIs
	// spawn a fresh process per command, so the recorded pid is usually dead.
	const deadPid = 2147480000

	cases := []struct {
		name        string
		expiresInMs int64
		host        string
		wantExpired bool
		wantStale   bool
	}{
		{"live lease, dead pid, this host", 60_000, util.HostID(), false, false},
		{"expired inside grace, dead pid, this host", -1, util.HostID(), true, true},
		{"expired inside grace, dead pid, other host", -1, "someone-else", true, false},
		{"expired past grace, other host", -(grace + 5_000), "someone-else", true, true},
		{"live lease, other host", 60_000, "someone-else", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			claim := jsonx.Obj{
				"id":               util.NewID("clm"),
				"host":             tc.host,
				"expiresAtInitial": util.NowISO(msTime(now + tc.expiresInMs)),
				"process":          jsonx.Obj{"pid": float64(deadPid)},
			}
			d := Decorate(ws, claim)
			if d.Expired != tc.wantExpired {
				t.Errorf("expired=%v, want %v", d.Expired, tc.wantExpired)
			}
			if d.Stale != tc.wantStale {
				t.Errorf("stale=%v, want %v", d.Stale, tc.wantStale)
			}
		})
	}

	// A lease record overrides the claim's own initial expiry.
	claim := jsonx.Obj{
		"id":               util.NewID("clm"),
		"host":             "someone-else",
		"expiresAtInitial": util.NowISO(msTime(now - grace - 60_000)),
		"process":          jsonx.Obj{"pid": float64(deadPid)},
	}
	if err := SaveClaim(ws, claim); err != nil {
		t.Fatal(err)
	}
	if !Decorate(ws, claim).Stale {
		t.Fatal("a long-expired claim is not stale")
	}
	if _, err := WriteLease(ws, claim.Str("id"), "ses_x", 60_000, 1); err != nil {
		t.Fatal(err)
	}
	d := Decorate(ws, claim)
	if d.Expired || d.Stale {
		t.Errorf("an extended lease did not revive the claim: expired=%v stale=%v", d.Expired, d.Stale)
	}
	if d.Lease == nil || d.EffectiveExpiresAt != d.Lease.Str("expiresAt") {
		t.Error("the lease is not the effective expiry")
	}
}

func TestListClaimsHidesStaleCardsUnlessAsked(t *testing.T) {
	ws := newWorkspace(t, "list-claims")
	live := jsonx.Obj{
		"id": util.NewID("clm"), "host": "elsewhere", "createdAt": util.Now(),
		"expiresAtInitial": util.NowISO(msTime(util.NowMs() + 60_000)),
		"process":          jsonx.Obj{"pid": float64(1)},
	}
	dead := jsonx.Obj{
		"id": util.NewID("clm"), "host": "elsewhere", "createdAt": util.Now(),
		"expiresAtInitial": util.NowISO(msTime(util.NowMs() - ws.GraceMs() - 60_000)),
		"process":          jsonx.Obj{"pid": float64(1)},
	}
	if err := SaveClaim(ws, live); err != nil {
		t.Fatal(err)
	}
	if err := SaveClaim(ws, dead); err != nil {
		t.Fatal(err)
	}
	if n := len(ListClaims(ws, false)); n != 1 {
		t.Errorf("%d live claims, want 1", n)
	}
	if n := len(ListClaims(ws, true)); n != 2 {
		t.Errorf("%d claims including stale, want 2", n)
	}
	// A file that is not JSON must not take the whole door down.
	if err := os.WriteFile(filepath.Join(ws.Paths.Claims, "clm_broken.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n := len(ListClaims(ws, true)); n != 2 {
		t.Errorf("a corrupt claim changed the listing to %d", n)
	}
}

func TestArchiveClaimClearsTheLeaseAndTheQueue(t *testing.T) {
	ws := newWorkspace(t, "archive")
	claim := jsonx.Obj{
		"id": util.NewID("clm"), "host": util.HostID(), "createdAt": util.Now(), "state": "active",
		"expiresAtInitial": util.NowISO(msTime(util.NowMs() + 60_000)),
		"process":          jsonx.Obj{"pid": float64(os.Getpid())},
	}
	if err := SaveClaim(ws, claim); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteLease(ws, claim.Str("id"), "ses_x", 60_000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteQueueEntry(ws, jsonx.Obj{"id": util.NewID("wai"), "claimId": claim.Str("id"), "createdAt": util.Now()}); err != nil {
		t.Fatal(err)
	}
	if len(ListQueue(ws, claim.Str("id"))) != 1 {
		t.Fatal("the waiter was not recorded")
	}
	archived, err := ArchiveClaim(ws, claim, "released")
	if err != nil {
		t.Fatal(err)
	}
	if archived.Str("state") != "released" {
		t.Errorf("archived state is %q", archived.Str("state"))
	}
	if fsx.Exists(ClaimFile(ws, claim.Str("id"))) {
		t.Error("the claim is still on the door")
	}
	if fsx.Exists(LeaseFile(ws, claim.Str("id"))) {
		t.Error("the lease outlived its claim")
	}
	if n := len(ListQueue(ws, claim.Str("id"))); n != 0 {
		t.Errorf("%d waiters left behind", n)
	}
	if !fsx.Exists(filepath.Join(ws.Paths.Archive, claim.Str("id")+".json")) {
		t.Error("nothing was archived")
	}
}

func TestReapOnlySweepsWhatIsTrulyGone(t *testing.T) {
	ws := newWorkspace(t, "reap")
	res, _ := JoinActor(ws, "alice", "other")
	mk := func(offset int64) jsonx.Obj {
		c := jsonx.Obj{
			"id": util.NewID("clm"), "host": "elsewhere", "createdAt": util.Now(), "state": "active",
			"actorName":        "ghost",
			"expiresAtInitial": util.NowISO(msTime(util.NowMs() + offset)),
			"process":          jsonx.Obj{"pid": float64(1)},
			"scope":            jsonx.Obj{"patterns": jsonx.Arr{"a/**"}},
		}
		if err := SaveClaim(ws, c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	live := mk(60_000)
	inGrace := mk(-1)
	longGone := mk(-(ws.GraceMs() + 60_000))

	swept, err := ReapStale(ws, res.Actor, res.Session, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(swept) != 1 || swept[0].Str("id") != longGone.Str("id") {
		t.Fatalf("plain reap swept %d claims (%v), want only the long-gone one", len(swept), ids(swept))
	}
	forced, err := ReapStale(ws, res.Actor, res.Session, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(forced) != 1 || forced[0].Str("id") != inGrace.Str("id") {
		t.Fatalf("reap --force swept %v, want the in-grace claim", ids(forced))
	}
	if !fsx.Exists(ClaimFile(ws, live.Str("id"))) {
		t.Error("reap took a live card off the door")
	}
}

func ids(list []jsonx.Obj) []string {
	out := []string{}
	for _, v := range list {
		out = append(out, v.Str("id"))
	}
	return out
}

// msTime turns a wall-clock millisecond into the time the ISO helpers expect.
func msTime(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// code names the error, or "" when the error is not one of ours.
func code(err error) string {
	if e := errs.As(err); e != nil {
		return e.Code
	}
	if err == nil {
		return ""
	}
	return err.Error()
}

func readText(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
