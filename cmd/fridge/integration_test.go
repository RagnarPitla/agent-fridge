// SPDX-License-Identifier: Apache-2.0
// These run the real binary in real child processes: exit codes here are the
// exact contract that AGENTS.md promises to every vendor. Mirrors
// test/integration/cli.test.mjs case for case.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
)

// TestHelperProcess is re-executed as the child of `fridge run`. It is skipped
// during a normal test run.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv("GO_FRIDGE_HELPER") {
	case "":
		t.Skip("not a helper invocation")
	case "echo":
		fmt.Println("child ran")
	case "exit7":
		os.Exit(7)
	case "forbidden":
		fmt.Println("must not run")
	}
	os.Exit(0)
}

func helperCmd(t *testing.T) []string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return []string{self, "-test.run=^TestHelperProcess$"}
}

func TestInitCreatesAGitFriendlySelfDescribingWorkspace(t *testing.T) {
	root := makeRepo(t, "init")
	r := fridge(t, root, []string{"init"})
	if r.Code != 0 {
		t.Fatalf("init exited %d: %s", r.Code, r.Stderr)
	}
	state := filepath.Join(root, ".fridge")
	for _, f := range []string{"VERSION", "config.json", "workspace.json", ".gitignore", "DOOR.md"} {
		if !exists(filepath.Join(state, f)) {
			t.Errorf("%s should exist", f)
		}
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(state, "VERSION"))); got != "wcp/0.1" {
		t.Errorf("VERSION = %q", got)
	}
	ignore := readFile(t, filepath.Join(state, ".gitignore"))
	if !strings.Contains(ignore, "!/notes/") {
		t.Errorf("shared history must be committed:\n%s", ignore)
	}
	if !regexp.MustCompile(`(?m)^/\*$`).MatchString(ignore) {
		t.Errorf("live state must be ignored by default:\n%s", ignore)
	}
	if !strings.Contains(readFile(t, filepath.Join(root, "AGENTS.md")), "fridge claim") {
		t.Errorf("AGENTS.md does not carry the rules")
	}
	if !strings.Contains(readFile(t, filepath.Join(root, ".gitattributes")), "notes") {
		t.Errorf(".gitattributes does not mention notes")
	}
	if code := fridge(t, root, []string{"init"}).Code; code != 15 {
		t.Errorf("second init exited %d, want 15 (E_ALREADY_EXISTS)", code)
	}
}

func TestCommandsRefuseToGuess(t *testing.T) {
	root := makeRepo(t, "bare")
	if code := fridge(t, root, []string{"board"}).Code; code != 3 {
		t.Errorf("board without a workspace exited %d, want 3", code)
	}
	fridge(t, root, []string{"init", "--no-adapters"})
	if code := fridge(t, root, []string{"claim", "src/**", "--task", "x"}).Code; code != 7 {
		t.Errorf("claim without a session exited %d, want 7", code)
	}
	fridge(t, root, []string{"join", "--agent", "alice"})
	fridge(t, root, []string{"join", "--agent", "bob"})
	ambiguous := fridge(t, root, []string{"claim", "src/**", "--task", "x"})
	if ambiguous.Code != 7 {
		t.Errorf("two housemates and no --agent exited %d, want 7", ambiguous.Code)
	}
	if !strings.Contains(ambiguous.Stderr, "--agent") {
		t.Errorf("the error must name --agent: %s", ambiguous.Stderr)
	}
}

func TestFullHappyPath(t *testing.T) {
	root := bootstrap(t, "happy", "alice")
	c := fridge(t, root, []string{"claim", "src/api/**", "--task", "refactor routes", "--json"}, runOpts{Actor: "alice"})
	if c.Code != 0 {
		t.Fatalf("claim exited %d: %s", c.Code, c.Stderr)
	}
	if !c.JSON.Bool("ok") || c.JSON.Str("protocol") != "wcp/0.1" {
		t.Errorf("envelope was %v", c.JSON)
	}
	id := c.JSON.Str("data.claimId")
	if !regexp.MustCompile(`^clm_[0-9A-HJKMNP-TV-Z]{26}$`).MatchString(id) {
		t.Errorf("claim id %q is not a ULID", id)
	}
	if n := len(c.JSON.ArrAt("data.scope.materialized")); n != 2 {
		t.Errorf("materialized %d files, want 2 under src/api", n)
	}
	if code := fridge(t, root, []string{"pin", "rewrote the retry loop"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("pin exited %d", code)
	}
	if !strings.Contains(readDoor(t, root), "refactor routes") {
		t.Errorf("the door does not show the task")
	}
	logOut := fridge(t, root, []string{"log", "--limit", "20"}, runOpts{Actor: "alice"}).Stdout
	if !strings.Contains(logOut, "rewrote the retry loop") {
		t.Errorf("log does not show the note:\n%s", logOut)
	}
	rel := fridge(t, root, []string{"release", id, "--outcome", "done", "--note", "green", "--json"}, runOpts{Actor: "alice"})
	if rel.Code != 0 || len(rel.JSON.ArrAt("data.released")) != 1 {
		t.Errorf("release exited %d with %v", rel.Code, rel.JSON.Get("data.released"))
	}
	st := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "alice"})
	if len(st.JSON.ArrAt("data.claims")) != 0 {
		t.Errorf("the card is still on the door")
	}
	if !exists(filepath.Join(root, ".fridge", "archive", "claims", id+".json")) {
		t.Errorf("released claims are archived, not deleted")
	}
}

func TestOverlappingScopesAreRefused(t *testing.T) {
	root := bootstrap(t, "conflict", "alice", "bob")
	if code := fridge(t, root, []string{"claim", "src/api/**", "--task", "a"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Fatalf("alice could not claim: %d", code)
	}
	cases := []struct{ path, why string }{
		{"src/api/routes.ts", "a single file inside a claimed tree"},
		{"src/api/**", "the identical tree"},
		{"src/**", "a parent tree"},
		{"src/api", "the directory itself"},
	}
	for _, c := range cases {
		r := fridge(t, root, []string{"claim", c.path, "--task", "b", "--json"}, runOpts{Actor: "bob"})
		if r.Code != 10 {
			t.Errorf("should refuse %s, exited %d", c.why, r.Code)
			continue
		}
		if r.JSON.Str("error.code") != "E_CONFLICT" {
			t.Errorf("%s: error code %q", c.why, r.JSON.Str("error.code"))
		}
		conflicts := r.JSON.ArrAt("error.details.conflicts")
		if len(conflicts) == 0 {
			t.Errorf("%s: no conflicts reported", c.why)
			continue
		}
		first, _ := conflicts[0].(jsonx.Obj)
		if first.Str("actorName") != "alice" {
			t.Errorf("%s: blamed %q", c.why, first.Str("actorName"))
		}
	}
	if code := fridge(t, root, []string{"claim", "src/ui/**", "--task", "b"}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Errorf("a sibling tree should be fine, exited %d", code)
	}
	if code := fridge(t, root, []string{"claim", "docs/**", "--task", "c"}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Errorf("docs should be fine, exited %d", code)
	}
}

func TestCheckAndGuard(t *testing.T) {
	root := bootstrap(t, "check", "alice", "bob")
	fridge(t, root, []string{"claim", "src/api/**", "--task", "a"}, runOpts{Actor: "alice"})
	if code := fridge(t, root, []string{"check", "src/api/routes.ts"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("mine exited %d", code)
	}
	if code := fridge(t, root, []string{"check", "src/api/routes.ts"}, runOpts{Actor: "bob"}).Code; code != 10 {
		t.Errorf("theirs exited %d, want 10", code)
	}
	if code := fridge(t, root, []string{"check", "docs/guide.md"}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Errorf("unclaimed exited %d, want 0", code)
	}
	if code := fridge(t, root, []string{"check", "docs/guide.md", "--for-write"}, runOpts{Actor: "bob"}).Code; code != 14 {
		t.Errorf("--for-write on unclaimed exited %d, want 14", code)
	}
	g := fridge(t, root, []string{"guard", "src/api/db.ts", "--json"}, runOpts{Actor: "bob"})
	if g.Code != 10 {
		t.Fatalf("guard exited %d, want 10", g.Code)
	}
	paths := g.JSON.ArrAt("error.details.paths")
	if len(paths) == 0 {
		t.Fatalf("guard reported no paths")
	}
	first, _ := paths[0].(jsonx.Obj)
	if first.Str("status") != "theirs" {
		t.Errorf("status = %q", first.Str("status"))
	}
}

func TestModes(t *testing.T) {
	root := bootstrap(t, "modes", "alice", "bob")
	fridge(t, root, []string{"claim", "docs/**", "--task", "read", "--mode", "shared"}, runOpts{Actor: "alice"})
	if code := fridge(t, root, []string{"claim", "docs/**", "--task", "read too", "--mode", "shared"}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Errorf("shared + shared exited %d", code)
	}
	if code := fridge(t, root, []string{"claim", "docs/**", "--task", "rewrite", "--mode", "exclusive"}, runOpts{Actor: "bob"}).Code; code != 10 {
		t.Errorf("shared + exclusive exited %d, want 10", code)
	}
	fridge(t, root, []string{"claim", "src/ui/**", "--task", "watching", "--mode", "advisory"}, runOpts{Actor: "alice"})
	if code := fridge(t, root, []string{"claim", "src/ui/**", "--task", "editing", "--mode", "exclusive"}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Errorf("advisory must never block, exited %d", code)
	}
}

func TestOwnershipIsEnforced(t *testing.T) {
	root := bootstrap(t, "ownership", "alice", "bob")
	id := fridge(t, root, []string{"claim", "src/api/**", "--task", "a", "--json"}, runOpts{Actor: "alice"}).JSON.Str("data.claimId")
	for _, args := range [][]string{
		{"release", id},
		{"extend", id, "--ttl", "1h"},
		{"handoff", id, "--to", "alice"},
	} {
		if code := fridge(t, root, args, runOpts{Actor: "bob"}).Code; code != 12 {
			t.Errorf("%v exited %d, want 12", args, code)
		}
	}
	if code := fridge(t, root, []string{"release", id, "--force"}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Errorf("--force is the documented human override, exited %d", code)
	}
	forced := false
	for _, n := range notes(t, root) {
		if n.Str("type") == "claim.released" && n.Bool("data.forced") {
			forced = true
		}
	}
	if !forced {
		t.Errorf("a forced release must be recorded")
	}
}

func TestHandoffKeepsTheCardOwnedAtEveryMoment(t *testing.T) {
	root := bootstrap(t, "handoff", "alice", "bob")
	id := fridge(t, root, []string{"claim", "src/api/**", "--task", "a", "--json"}, runOpts{Actor: "alice"}).JSON.Str("data.claimId")
	h := fridge(t, root, []string{"handoff", id, "--to", "bob", "--note", "tests are red", "--json"}, runOpts{Actor: "alice"})
	if h.Code != 0 {
		t.Fatalf("handoff exited %d: %s", h.Code, h.Stderr)
	}
	if code := fridge(t, root, []string{"claim", "src/api/**", "--task", "sneak"}, runOpts{Actor: "bob"}).Code; code != 10 {
		t.Errorf("an offered card is still held, exited %d", code)
	}
	inbox := fridge(t, root, []string{"inbox", "--json"}, runOpts{Actor: "bob"})
	msgs := inbox.JSON.ArrAt("data.messages")
	if len(msgs) != 1 {
		t.Fatalf("inbox has %d messages", len(msgs))
	}
	first, _ := msgs[0].(jsonx.Obj)
	if first.Str("note") != "tests are red" {
		t.Errorf("note = %q", first.Str("note"))
	}
	if code := fridge(t, root, []string{"accept", h.JSON.Str("data.messageId"), "--json"}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Fatalf("accept exited %d", code)
	}
	after := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "bob"}).JSON.ArrAt("data.claims")
	owner, _ := after[0].(jsonx.Obj)
	if owner.Str("actorName") != "bob" {
		t.Errorf("owner = %q", owner.Str("actorName"))
	}
	if code := fridge(t, root, []string{"release", id}, runOpts{Actor: "alice"}).Code; code != 12 {
		t.Errorf("alice no longer owns it, exited %d", code)
	}
	if code := fridge(t, root, []string{"release", id}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Errorf("bob could not release, exited %d", code)
	}
}

func TestDeclineLeavesTheCardWithTheOriginalOwner(t *testing.T) {
	root := bootstrap(t, "decline", "alice", "bob")
	id := fridge(t, root, []string{"claim", "src/api/**", "--task", "a", "--json"}, runOpts{Actor: "alice"}).JSON.Str("data.claimId")
	h := fridge(t, root, []string{"handoff", id, "--to", "bob", "--json"}, runOpts{Actor: "alice"})
	if code := fridge(t, root, []string{"decline", h.JSON.Str("data.messageId"), "--reason", "busy"}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Fatalf("decline exited %d", code)
	}
	claims := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "alice"}).JSON.ArrAt("data.claims")
	owner, _ := claims[0].(jsonx.Obj)
	if owner.Str("actorName") != "alice" {
		t.Errorf("owner = %q", owner.Str("actorName"))
	}
	if code := fridge(t, root, []string{"release", id}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("release exited %d", code)
	}
}

func TestExpiredLeaseIsSweptAndTheWorkCanBeTakenOver(t *testing.T) {
	root := bootstrap(t, "lease", "alice", "bob")
	fridge(t, root, []string{"claim", "src/api/**", "--task", "slow", "--ttl", "1s"}, runOpts{Actor: "alice"})
	if code := fridge(t, root, []string{"claim", "src/api/**", "--task", "takeover"}, runOpts{Actor: "bob"}).Code; code != 10 {
		t.Errorf("still live, exited %d", code)
	}
	time.Sleep(1400 * time.Millisecond)
	reaped := fridge(t, root, []string{"reap", "--json"}, runOpts{Actor: "bob"}).JSON.ArrAt("data.reaped")
	if len(reaped) != 1 {
		t.Errorf("reaped %d claims, want 1", len(reaped))
	}
	if code := fridge(t, root, []string{"claim", "src/api/**", "--task", "takeover"}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Errorf("takeover exited %d", code)
	}
	seen := false
	for _, n := range notes(t, root) {
		if n.Str("type") == "claim.expired" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("expiry must be recorded on the wall")
	}
}

func TestHeartbeatAndExtendKeepALongJobAlive(t *testing.T) {
	root := bootstrap(t, "heartbeat", "alice")
	id := fridge(t, root, []string{"claim", "src/api/**", "--task", "long", "--ttl", "30s", "--json"}, runOpts{Actor: "alice"}).JSON.Str("data.claimId")
	claims := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "alice"}).JSON.ArrAt("data.claims")
	first, _ := claims[0].(jsonx.Obj)
	before, err := time.Parse(time.RFC3339Nano, first.Str("expiresAt"))
	if err != nil {
		t.Fatal(err)
	}
	hb := fridge(t, root, []string{"heartbeat", "--json"}, runOpts{Actor: "alice"})
	if hb.Code != 0 {
		t.Fatalf("heartbeat exited %d: %s", hb.Code, hb.Stderr)
	}
	renewed := hb.JSON.ArrAt("data.renewed")
	if len(renewed) == 0 {
		t.Fatalf("nothing renewed")
	}
	r0, _ := renewed[0].(jsonx.Obj)
	if r0.Str("claimId") != id {
		t.Errorf("renewed %q want %q", r0.Str("claimId"), id)
	}
	after, err := time.Parse(time.RFC3339Nano, r0.Str("expiresAt"))
	if err != nil {
		t.Fatal(err)
	}
	if after.Before(before) {
		t.Errorf("heartbeat moved the expiry backwards")
	}
	ex := fridge(t, root, []string{"extend", id, "--ttl", "2h", "--json"}, runOpts{Actor: "alice"})
	if ex.Code != 0 {
		t.Fatalf("extend exited %d", ex.Code)
	}
	exAt, err := time.Parse(time.RFC3339Nano, ex.JSON.Str("data.expiresAt"))
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exAt) <= time.Hour {
		t.Errorf("extend did not push the expiry out two hours")
	}
}

func TestNotesAreWriteOnce(t *testing.T) {
	root := bootstrap(t, "notes", "alice", "bob")
	for _, who := range []string{"alice", "bob"} {
		for i := 0; i < 10; i++ {
			fridge(t, root, []string{"pin", fmt.Sprintf("%s note %d", who, i)}, runOpts{Actor: who})
		}
	}
	all := notes(t, root)
	pinned := []jsonx.Obj{}
	ids := map[string]bool{}
	for _, n := range all {
		if n.Str("type") == "note.note" {
			pinned = append(pinned, n)
		}
		ids[n.Str("id")] = true
	}
	if len(pinned) != 20 {
		t.Errorf("%d pinned notes, want 20", len(pinned))
	}
	if len(ids) != len(all) {
		t.Errorf("%d unique ids across %d notes", len(ids), len(all))
	}
	for _, who := range []string{"alice", "bob"} {
		for i := 0; i < 10; i++ {
			want := fmt.Sprintf("%s note %d", who, i)
			count := 0
			for _, n := range pinned {
				if n.Str("actorName") == who && n.Str("summary") == want {
					count++
				}
			}
			if count != 1 {
				t.Errorf("%q appears %d times", want, count)
			}
		}
	}
}

func TestNotesRefuseToBecomeASecretStore(t *testing.T) {
	root := bootstrap(t, "secrets", "alice")
	r := fridge(t, root, []string{"pin", "deployed with AKIAIOSFODNN7EXAMPLE"}, runOpts{Actor: "alice"})
	if r.Code != 2 {
		t.Errorf("exited %d, want 2", r.Code)
	}
	if !strings.Contains(r.Stderr, "AWS access key") {
		t.Errorf("stderr = %q", r.Stderr)
	}
	if code := fridge(t, root, []string{"pin", "deployed with AKIAIOSFODNN7EXAMPLE", "--allow-secret-like"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("--allow-secret-like exited %d", code)
	}
}

func TestTheDoorIsGeneratedAndDriftIsDetectable(t *testing.T) {
	root := bootstrap(t, "door", "alice")
	fridge(t, root, []string{"claim", "src/**", "--task", "work"}, runOpts{Actor: "alice"})
	if code := fridge(t, root, []string{"board", "--check"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("fresh door reports drift, exited %d", code)
	}
	if !strings.Contains(readDoor(t, root), "DO NOT EDIT") {
		t.Errorf("the door has no warning")
	}
	writeFile(t, filepath.Join(root, ".fridge", "DOOR.md"), "# I edited the door by hand\n")
	if code := fridge(t, root, []string{"board", "--check"}, runOpts{Actor: "alice"}).Code; code != 30 {
		t.Errorf("hand-edited door exited %d, want 30", code)
	}
	if code := fridge(t, root, []string{"render"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("render exited %d", code)
	}
	if code := fridge(t, root, []string{"board", "--check"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("after render, --check exited %d", code)
	}
	if !strings.Contains(readDoor(t, root), "Claimed right now") {
		t.Errorf("the generated view did not come back")
	}
}

func TestRunClaimsExecutesAndAlwaysTidiesUp(t *testing.T) {
	root := bootstrap(t, "run", "alice")
	child := helperCmd(t)

	args := append([]string{"run", "--claim", "src/**", "--task", "tests", "--"}, child...)
	ok := fridge(t, root, args, runOpts{Actor: "alice", Env: []string{"GO_FRIDGE_HELPER=echo"}})
	if ok.Code != 0 {
		t.Fatalf("run exited %d: %s", ok.Code, ok.Stderr)
	}
	if !strings.Contains(ok.Stdout, "child ran") {
		t.Errorf("the child's stdout did not come through:\n%s", ok.Stdout)
	}
	if n := len(fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "alice"}).JSON.ArrAt("data.claims")); n != 0 {
		t.Errorf("the card did not come down, %d left", n)
	}

	bad := fridge(t, root, args, runOpts{Actor: "alice", Env: []string{"GO_FRIDGE_HELPER=exit7"}})
	if bad.Code != 7 {
		t.Errorf("the child exit code must pass straight through, got %d", bad.Code)
	}
	if n := len(fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "alice"}).JSON.ArrAt("data.claims")); n != 0 {
		t.Errorf("a failing child still has to tidy up, %d left", n)
	}

	fridge(t, root, []string{"claim", "src/**", "--task", "blocker"}, runOpts{Actor: "alice"})
	fridge(t, root, []string{"join", "--agent", "bob"})
	blockedArgs := append([]string{"run", "--claim", "src/**", "--task", "x", "--"}, child...)
	blocked := fridge(t, root, blockedArgs, runOpts{Actor: "bob", Env: []string{"GO_FRIDGE_HELPER=forbidden"}})
	if blocked.Code != 10 {
		t.Errorf("blocked run exited %d, want 10", blocked.Code)
	}
	if strings.Contains(blocked.Stdout, "must not run") {
		t.Errorf("the command ran even though the claim failed")
	}
}

func TestWaitReturnsWhenTheCardComesDownAndTimesOutHonestly(t *testing.T) {
	root := bootstrap(t, "wait", "alice", "bob")
	id := fridge(t, root, []string{"claim", "src/**", "--task", "a", "--json"}, runOpts{Actor: "alice"}).JSON.Str("data.claimId")
	if code := fridge(t, root, []string{"wait", id, "--timeout", "1s"}, runOpts{Actor: "bob"}).Code; code != 21 {
		t.Errorf("wait exited %d, want 21", code)
	}
	fridge(t, root, []string{"release", id}, runOpts{Actor: "alice"})
	if code := fridge(t, root, []string{"wait", id, "--timeout", "5s"}, runOpts{Actor: "bob"}).Code; code != 11 {
		t.Errorf("waiting on a gone card exited %d, want 11", code)
	}
}

func TestPathsAreValidatedBeforeAnythingIsWritten(t *testing.T) {
	root := bootstrap(t, "paths", "alice")
	for _, bad := range []string{"../outside", "~/secrets", ".git/config", ".fridge/claims"} {
		if code := fridge(t, root, []string{"claim", bad, "--task", "x"}, runOpts{Actor: "alice"}).Code; code != 40 {
			t.Errorf("%s exited %d, want 40", bad, code)
		}
	}
	if code := fridge(t, root, []string{"claim", "**", "--task", "everything"}, runOpts{Actor: "alice"}).Code; code != 2 {
		t.Errorf("a whole-repo claim needs --confirm-global, exited %d", code)
	}
	if code := fridge(t, root, []string{"claim", "**", "--task", "everything", "--confirm-global"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("--confirm-global exited %d", code)
	}
}

func TestUsageErrorsAreExplicit(t *testing.T) {
	root := bootstrap(t, "usage", "alice")
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"claim", "src/**"}, 2},
		{[]string{"claim", "src/**", "--task", "x", "--bogus"}, 2},
		{[]string{"claim", "src/**", "--task", "x", "--ttl", "soon"}, 2},
		{[]string{"claim", "src/**", "--task", "x", "--mode", "maybe"}, 2},
		{[]string{"teleport"}, 2},
		{[]string{"release", "clm_00000000000000000000000000"}, 11},
	}
	for _, c := range cases {
		if code := fridge(t, root, c.args, runOpts{Actor: "alice"}).Code; code != c.want {
			t.Errorf("%v exited %d, want %d", c.args, code, c.want)
		}
	}
	help := fridge(t, root, []string{"claim", "--help"}, runOpts{Actor: "alice"})
	if help.Code != 0 {
		t.Errorf("--help exited %d", help.Code)
	}
	if !strings.Contains(help.Stdout, "exit codes:") {
		t.Errorf("help does not list exit codes:\n%s", help.Stdout)
	}
	if !regexp.MustCompile(`10\s+E_CONFLICT`).MatchString(help.Stdout) {
		t.Errorf("help does not mention E_CONFLICT:\n%s", help.Stdout)
	}
}

func TestEveryJSONResponseUsesTheSameEnvelope(t *testing.T) {
	root := bootstrap(t, "json", "alice")
	want := []string{"command", "data", "error", "exitCode", "ok", "protocol", "ts"}
	for _, args := range [][]string{{"version"}, {"whoami"}, {"status"}, {"board"}, {"log"}, {"inbox"}, {"doctor"}} {
		r := fridge(t, root, append(args, "--json"), runOpts{Actor: "alice"})
		if r.Code != 0 {
			t.Errorf("%s exited %d: %s", args[0], r.Code, r.Stderr)
			continue
		}
		if r.JSON == nil {
			t.Errorf("%s must emit parseable JSON, got:\n%s", args[0], r.Stdout)
			continue
		}
		got := []string{}
		for k := range r.JSON {
			got = append(got, k)
		}
		sortStrings(got)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s envelope keys %v", args[0], got)
		}
		if !r.JSON.Bool("ok") {
			t.Errorf("%s reported ok=false", args[0])
		}
		lines := strings.Split(strings.TrimRight(r.Stdout, "\n"), "\n")
		if !strings.HasSuffix(lines[len(lines)-1], "}") {
			t.Errorf("%s: stdout is not exactly one JSON object", args[0])
		}
	}
	e := fridge(t, root, []string{"claim", "../nope", "--task", "x", "--json"}, runOpts{Actor: "alice"})
	if e.JSON.Bool("ok") || e.JSON.Num("exitCode") != 40 || e.JSON.Str("error.code") != "E_PATH_INVALID" {
		t.Errorf("error envelope was %v", e.JSON)
	}
	if e.JSON.Str("error.hint") == "" {
		t.Errorf("errors must carry a next step")
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func TestOutputIsPlainASCII(t *testing.T) {
	root := bootstrap(t, "ascii", "alice")
	fridge(t, root, []string{"claim", "src/**", "--task", "unicode check"}, runOpts{Actor: "alice"})
	for _, args := range [][]string{{"board"}, {"status"}, {"log"}, {"whoami"}, {"doctor"}, {"version"}} {
		r := fridge(t, root, args, runOpts{Actor: "alice"})
		if strings.Contains(r.Stdout, "\x1b[") {
			t.Errorf("%s emitted ANSI escapes", args[0])
		}
		for _, ch := range r.Stdout {
			if ch > 126 {
				t.Errorf("%s emitted non-ASCII %q", args[0], ch)
				break
			}
		}
	}
}

func TestDoctorFindsDamageAndFixRepairsIt(t *testing.T) {
	root := bootstrap(t, "doctor", "alice")
	fridge(t, root, []string{"claim", "src/**", "--task", "x"}, runOpts{Actor: "alice"})
	writeFile(t, filepath.Join(root, ".fridge", "leases", "clm_ORPHAN.json"),
		"{\"schema\":\"wcp/0.1/lease\",\"claimId\":\"clm_ORPHAN\"}\n")
	writeFile(t, filepath.Join(root, ".fridge", "actors", "broken.json"), "{ this is not json")
	if err := os.Remove(filepath.Join(root, ".fridge", ".gitignore")); err != nil {
		t.Fatal(err)
	}
	found := fridge(t, root, []string{"doctor", "--check", "--json"}, runOpts{Actor: "alice"})
	if found.Code != 30 {
		t.Fatalf("doctor --check exited %d, want 30", found.Code)
	}
	ids := []string{}
	for _, raw := range found.JSON.ArrAt("error.details.findings") {
		f, _ := raw.(jsonx.Obj)
		ids = append(ids, f.Str("id"))
	}
	hasPrefix := func(p string) bool {
		for _, id := range ids {
			if strings.HasPrefix(id, p) {
				return true
			}
		}
		return false
	}
	if !hasPrefix("gitignore-missing") || !hasPrefix("orphan-lease:") || !hasPrefix("corrupt:") {
		t.Errorf("findings were %v", ids)
	}
	if code := fridge(t, root, []string{"doctor", "--fix"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Fatalf("doctor --fix exited %d", code)
	}
	if code := fridge(t, root, []string{"doctor", "--check"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("not clean after --fix, exited %d", code)
	}
	if !exists(filepath.Join(root, ".fridge", ".gitignore")) {
		t.Errorf(".gitignore was not restored")
	}
	entries, err := os.ReadDir(filepath.Join(root, ".fridge", "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("quarantine holds %d files, want 1", len(entries))
	}
}

func TestAdaptersWriteOneCanonicalBlock(t *testing.T) {
	root := bootstrap(t, "adapters-cli", "alice")
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "# House rules\n\nRun the tests.\n")
	if code := fridge(t, root, []string{"adapters", "check"}, runOpts{Actor: "alice"}).Code; code != 30 {
		t.Errorf("adapters check exited %d, want 30", code)
	}
	if code := fridge(t, root, []string{"adapters", "install", "--vendor", "all"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Fatalf("adapters install exited %d", code)
	}
	if code := fridge(t, root, []string{"adapters", "check"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("adapters check after install exited %d", code)
	}
	claude := readFile(t, filepath.Join(root, "CLAUDE.md"))
	if !strings.Contains(claude, "Run the tests.") {
		t.Errorf("existing instructions were lost")
	}
	if !strings.Contains(claude, "BEGIN WCP-ADAPTER") {
		t.Errorf("no adapter block was written")
	}
	for _, f := range []string{"AGENTS.md", "CLAUDE.md", ".github/copilot-instructions.md", ".codex/instructions.md", "docs/AGENT-COORDINATION.md"} {
		if !strings.Contains(readFile(t, filepath.Join(root, filepath.FromSlash(f))), "fridge claim") {
			t.Errorf("%s does not carry the rules", f)
		}
	}
	if code := fridge(t, root, []string{"adapters", "install", "--vendor", "all"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("install is not idempotent, exited %d", code)
	}
	if code := fridge(t, root, []string{"adapters", "check"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("adapters check exited %d", code)
	}
}

func TestMigrateImportsLegacyMarkdown(t *testing.T) {
	root := bootstrap(t, "migrate", "alice")
	writeFile(t, filepath.Join(root, "To-do.done.md"), "# Done\n\n- shipped the parser\n- fixed the retry loop\n")
	writeFile(t, filepath.Join(root, "shared-development-updates.md"), "## agent-a\n\n- owns src/api\n- 128 lines rewritten\n")
	dry := fridge(t, root, []string{"migrate", "--dry-run", "--json"}, runOpts{Actor: "alice"})
	if dry.Code != 0 {
		t.Fatalf("migrate --dry-run exited %d: %s", dry.Code, dry.Stderr)
	}
	if dry.JSON.Num("data.count") != 4 {
		t.Errorf("counted %v entries, want 4", dry.JSON.Get("data.count"))
	}
	for _, n := range notes(t, root) {
		if strings.HasPrefix(n.Str("type"), "legacy.") {
			t.Fatalf("--dry-run wrote a note")
		}
	}
	if code := fridge(t, root, []string{"migrate", "--freeze"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Fatalf("migrate --freeze exited %d", code)
	}
	imported := []jsonx.Obj{}
	for _, n := range notes(t, root) {
		if strings.HasPrefix(n.Str("type"), "legacy.") {
			imported = append(imported, n)
		}
	}
	if len(imported) != 4 {
		t.Errorf("imported %d entries, want 4", len(imported))
	}
	seen := false
	for _, n := range imported {
		if n.Str("summary") == "128 lines rewritten" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("the headline line was not imported")
	}
	if !strings.HasPrefix(readFile(t, filepath.Join(root, "To-do.done.md")), "<!-- FROZEN") {
		t.Errorf("--freeze did not stamp the legacy file")
	}
}

func TestConfigIsReadableWritableAndRejectsNonsense(t *testing.T) {
	root := bootstrap(t, "config", "alice")
	if got := strings.TrimSpace(fridge(t, root, []string{"config", "lease.defaultTtlMs"}, runOpts{Actor: "alice"}).Stdout); got != "900000" {
		t.Errorf("config read = %q", got)
	}
	if code := fridge(t, root, []string{"config", "policy.requireTaskOnClaim", "false"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Fatalf("config write exited %d", code)
	}
	if code := fridge(t, root, []string{"claim", "src/**"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("--task should be optional once policy says so, exited %d", code)
	}
	if code := fridge(t, root, []string{"config", "lease.defaultTtlMs", "ages"}, runOpts{Actor: "alice"}).Code; code != 2 {
		t.Errorf("nonsense value exited %d, want 2", code)
	}
	if code := fridge(t, root, []string{"config", "nope.nope"}, runOpts{Actor: "alice"}).Code; code != 11 {
		t.Errorf("unknown key exited %d, want 11", code)
	}
}

func TestTTLAboveTheMaximumIsCappedWithAWarning(t *testing.T) {
	root := bootstrap(t, "ttl-cap", "alice")
	r := fridge(t, root, []string{"claim", "src/**", "--task", "forever", "--ttl", "7d", "--json"}, runOpts{Actor: "alice"})
	if r.Code != 0 {
		t.Fatalf("claim exited %d", r.Code)
	}
	if r.JSON.Num("data.ttlMs") != 14400000 {
		t.Errorf("ttlMs = %v, want the lease.maxTtlMs cap", r.JSON.Get("data.ttlMs"))
	}
	if !strings.Contains(r.Stderr, "capped") {
		t.Errorf("capping must be announced, stderr = %q", r.Stderr)
	}
}

func TestAFutureProtocolIsRefused(t *testing.T) {
	root := bootstrap(t, "protocol", "alice")
	writeFile(t, filepath.Join(root, ".fridge", "VERSION"), "wcp/9.9\n")
	r := fridge(t, root, []string{"board"}, runOpts{Actor: "alice"})
	if r.Code != 4 {
		t.Errorf("exited %d, want 4", r.Code)
	}
	if !strings.Contains(r.Stderr, "wcp/9.9") {
		t.Errorf("the error must name the version it found: %q", r.Stderr)
	}
}

func TestQueuePutsYouOnTheWaitingList(t *testing.T) {
	root := bootstrap(t, "queue", "alice", "bob")
	held := fridge(t, root, []string{"claim", "src/api/**", "--task", "refactor", "--json"}, runOpts{Actor: "alice"})
	if held.Code != 0 {
		t.Fatalf("claim exited %d", held.Code)
	}
	claimID := held.JSON.Str("data.claimId")
	denied := fridge(t, root, []string{"claim", "src/api/routes.ts", "--task", "typo", "--queue", "--json"}, runOpts{Actor: "bob"})
	if denied.Code != 10 {
		t.Errorf("still an honest refusal, exited %d", denied.Code)
	}
	if len(denied.JSON.ArrAt("error.details.queued")) != 1 {
		t.Errorf("no place in the line: %v", denied.JSON.Get("error.details"))
	}
	queueDir := filepath.Join(root, ".fridge", "queue")
	if n := countJSON(t, queueDir); n != 1 {
		t.Errorf("queue holds %d entries, want 1", n)
	}
	if got := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "bob"}).JSON.Num("data.waiting"); got != 1 {
		t.Errorf("the board shows waiting=%v", got)
	}
	joined := 0
	for _, n := range notes(t, root) {
		if n.Str("type") == "queue.joined" {
			joined++
		}
	}
	if joined != 1 {
		t.Errorf("%d queue.joined notes", joined)
	}
	if code := fridge(t, root, []string{"release", claimID, "--outcome", "done"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Fatalf("release exited %d", code)
	}
	if n := countJSON(t, queueDir); n != 0 {
		t.Errorf("releasing must clear the line, %d left", n)
	}
	if code := fridge(t, root, []string{"claim", "src/api/routes.ts", "--task", "typo"}, runOpts{Actor: "bob"}).Code; code != 0 {
		t.Errorf("bob could not take it, exited %d", code)
	}
}

func countJSON(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			n++
		}
	}
	return n
}

func TestReapForceSweepsAnExpiredCardWhoseOwnerIsStillRunning(t *testing.T) {
	root := bootstrap(t, "reap-force", "alice", "bob")
	fridge(t, root, []string{"config", "lease.graceMs", "3600000"}, runOpts{Actor: "alice"})
	held := fridge(t, root, []string{"claim", "src/api/**", "--task", "slow", "--ttl", "1s", "--json"}, runOpts{Actor: "alice"})
	if held.Code != 0 {
		t.Fatalf("claim exited %d", held.Code)
	}
	claimID := held.JSON.Str("data.claimId")

	// Pin the claim to a process that really is alive (this test), so the only
	// thing that has run out is the lease. That is the case grace exists for.
	file := filepath.Join(root, ".fridge", "claims", claimID+".json")
	claim := readJSONFile(t, file)
	proc := claim.ObjAt("process")
	proc["pid"] = float64(os.Getpid())
	claim["process"] = proc
	writeFile(t, file, jsonx.Stable(claim))
	time.Sleep(1400 * time.Millisecond)

	plain := fridge(t, root, []string{"reap", "--json"}, runOpts{Actor: "bob"})
	if n := len(plain.JSON.ArrAt("data.reaped")); n != 0 {
		t.Errorf("grace must protect a live owner, reaped %d", n)
	}
	forced := fridge(t, root, []string{"reap", "--force", "--json"}, runOpts{Actor: "bob"})
	if forced.Code != 0 {
		t.Fatalf("reap --force exited %d: %s", forced.Code, forced.Stderr)
	}
	if n := len(forced.JSON.ArrAt("data.reaped")); n != 1 {
		t.Errorf("force must sweep it anyway, reaped %d", n)
	}
	count := 0
	for _, n := range notes(t, root) {
		if n.Str("type") == "claim.expired" && n.Bool("data.forced") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("%d forced expiry notes, want 1", count)
	}
}

func TestRenderOutputWritesACommittableCopy(t *testing.T) {
	root := bootstrap(t, "render-out", "alice")
	fridge(t, root, []string{"claim", "docs/**", "--task", "write the guide"}, runOpts{Actor: "alice"})
	r := fridge(t, root, []string{"render", "--output", "TEAM-BOARD.md"}, runOpts{Actor: "alice"})
	if r.Code != 0 {
		t.Fatalf("render exited %d: %s", r.Code, r.Stderr)
	}
	copyText := readFile(t, filepath.Join(root, "TEAM-BOARD.md"))
	if !strings.Contains(copyText, "DO NOT EDIT") || !strings.Contains(copyText, "write the guide") {
		t.Errorf("the copy is not the door:\n%s", copyText)
	}
}

func TestMigrateCreditsTheOriginalAuthor(t *testing.T) {
	root := bootstrap(t, "migrate-authors", "alice", "copilot")
	writeFile(t, filepath.Join(root, "shared-development-updates.md"),
		"## updates\n\n- copilot: owns src/ui this afternoon\n- legacy-bot: rewrote the parser\n- nobody in particular wrote this line\n")
	r := fridge(t, root, []string{"migrate", "--updates", "shared-development-updates.md", "--author-map", "legacy-bot=alice", "--json"},
		runOpts{Actor: "alice"})
	if r.Code != 0 {
		t.Fatalf("migrate exited %d: %s", r.Code, r.Stderr)
	}
	imported := []jsonx.Obj{}
	for _, n := range notes(t, root) {
		if n.Str("type") == "legacy.update" {
			imported = append(imported, n)
		}
	}
	by := func(needle string) jsonx.Obj {
		for _, n := range imported {
			if strings.Contains(n.Str("data.body"), needle) {
				return n
			}
		}
		t.Fatalf("no imported note contains %q", needle)
		return nil
	}
	if got := by("owns src/ui").Str("actorName"); got != "copilot" {
		t.Errorf("a known actor must keep their name, got %q", got)
	}
	if got := by("rewrote the parser").Str("actorName"); got != "alice" {
		t.Errorf("--author-map was not honoured, got %q", got)
	}
	unknown := by("nobody in particular")
	if got := unknown.Str("actorName"); got != "alice" {
		t.Errorf("an unknown author must fall back to the importer, got %q", got)
	}
	if unknown.Get("data.detectedAuthor") != nil {
		t.Errorf("detectedAuthor should be null, got %v", unknown.Get("data.detectedAuthor"))
	}
}

func TestForeignHostIsNotForceReleasedByAccident(t *testing.T) {
	root := bootstrap(t, "foreign", "alice", "bob")
	held := fridge(t, root, []string{"claim", "src/api/**", "--task", "remote work", "--json"}, runOpts{Actor: "alice"})
	claimID := held.JSON.Str("data.claimId")
	file := filepath.Join(root, ".fridge", "claims", claimID+".json")
	claim := readJSONFile(t, file)
	claim["host"] = "sha256:some-other-machine"
	writeFile(t, file, jsonx.Stable(claim))

	r := fridge(t, root, []string{"release", claimID, "--force", "--outcome", "abandoned"}, runOpts{Actor: "bob"})
	if r.Code != 41 {
		t.Errorf("exited %d, want 41 (E_FOREIGN_HOST)", r.Code)
	}
	if !strings.Contains(r.Stderr, "another machine") {
		t.Errorf("stderr = %q", r.Stderr)
	}
	if code := fridge(t, root, []string{"release", claimID, "--force", "--allow-multihost", "--outcome", "abandoned"},
		runOpts{Actor: "bob"}).Code; code != 0 {
		t.Errorf("--allow-multihost exited %d", code)
	}
}
