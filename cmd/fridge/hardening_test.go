// SPDX-License-Identifier: Apache-2.0
// The Go half of the issue #1 regressions. Every case here has a counterpart in
// test/unit/hardening.test.mjs, because a fix that only landed in one
// implementation is a fix that will be undone by the other.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/fsx"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/paths"
	"github.com/RagnarPitla/agent-fridge/internal/secrets"
	"github.com/RagnarPitla/agent-fridge/internal/store"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

func scope(includes ...string) paths.Scope {
	return paths.Scope{Include: includes, Exclude: []string{}}
}

// overlaps is ScopesOverlap with the case-sensitivity argument pinned and any
// error surfaced, so each case below reads as the question it is really asking.
func overlaps(t *testing.T, a, b paths.Scope) paths.Overlap {
	t.Helper()
	got, err := paths.ScopesOverlap(a, b, false)
	if err != nil {
		t.Fatalf("ScopesOverlap(%v, %v): %v", a.Include, b.Include, err)
	}
	return got
}

func covers(t *testing.T, outer, inner string) bool {
	t.Helper()
	got, err := paths.PatternCovers(outer, inner, false)
	if err != nil {
		t.Fatalf("PatternCovers(%q, %q): %v", outer, inner, err)
	}
	return got
}

func claimIDOf(t *testing.T, r runResult) string {
	t.Helper()
	id := r.JSON.Str("data.claimId")
	if id == "" {
		t.Fatalf("no claimId in %s", r.Stdout)
	}
	return id
}

func liveClaims(t *testing.T, root, actor string) jsonx.Arr {
	t.Helper()
	return fridge(t, root, []string{"status", "--json"}, runOpts{Actor: actor}).JSON.ArrAt("data.claims")
}

func readObj(t *testing.T, p string) jsonx.Obj {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	o, err := jsonx.ParseObj(b)
	if err != nil {
		t.Fatalf("parsing %s: %v", p, err)
	}
	return o
}

// ------------------------------------------------------------------ 1. globs

func TestOverlapIsDecidedOnPatternsNotOnFilesThatHappenToExist(t *testing.T) {
	colliding := [][2]string{
		{"*.md", "CHANGELOG.md"},
		{"a*/x.ts", "*b/x.ts"},
		{"src/*.ts", "src/a?.ts"},
		{"{src,docs}/**", "docs/guide.md"},
		{"src/[ab]/x.ts", "src/[bc]/x.ts"},
		{"[a-z].md", "b.md"},
		{"[a-m].md", "[h-z].md"},
		{"[!a-z].md", "7.md"},
	}
	for _, p := range colliding {
		got := overlaps(t, scope(p[0]), scope(p[1]))
		if !got.Overlap {
			t.Errorf("%s and %s can both match a future path but were called disjoint", p[0], p[1])
		}
		if got.Reason == "" {
			t.Errorf("%s vs %s came back without a reason", p[0], p[1])
		}
	}

	disjoint := [][2]string{
		{"src/api/*.ts", "src/api/*.js"},
		{"src/**", "docs/**"},
		{"src/[ab]/x.ts", "src/[cd]/x.ts"},
		{"[a-f].md", "[x-z].md"},
		{"[!a-z].md", "b.md"},
		{"[a-z].md", "[!a-z].md"},
		{"README.md", "src/**"},
		{"*.ts", "src/**"},
	}
	for _, p := range disjoint {
		if overlaps(t, scope(p[0]), scope(p[1])).Overlap {
			t.Errorf("%s and %s cannot share a path but were refused", p[0], p[1])
		}
	}
}

func TestASecondClaimOnAFileNobodyHasCreatedYetIsRefused(t *testing.T) {
	root := bootstrap(t, "gh-glob", "alice", "bob")
	if c := fridge(t, root, []string{"claim", "*.md", "--task", "docs", "--json"}, runOpts{Actor: "alice"}).Code; c != 0 {
		t.Fatalf("first claim exited %d", c)
	}
	r := fridge(t, root, []string{"claim", "CHANGELOG.md", "--task", "changelog", "--json"}, runOpts{Actor: "bob"})
	if r.Code != 10 {
		t.Fatalf("a future CHANGELOG.md would have had two owners: exit %d, %s", r.Code, r.Stdout)
	}
	if got := r.JSON.Str("error.code"); got != "E_CONFLICT" {
		t.Fatalf("error code = %q", got)
	}
	if n := len(liveClaims(t, root, "alice")); n != 1 {
		t.Fatalf("board shows %d cards, want 1", n)
	}
}

func TestBraceExpansionIsBoundedRatherThanAllowedToExplode(t *testing.T) {
	bomb := strings.Repeat("{a,b}", 12) + "/x.ts"
	if _, err := paths.PatternsCanIntersect(bomb, "src/**", false); err == nil {
		t.Fatal("a brace bomb was expanded instead of refused")
	}
}

func TestAnExcludeOnlyCancelsAPairWhenItSwallowsTheOtherSideWhole(t *testing.T) {
	if !covers(t, "src/vendor/**", "src/vendor/lib/a.ts") {
		t.Error("src/vendor/** should cover src/vendor/lib/a.ts")
	}
	if covers(t, "src/vendor/**", "src/**") {
		t.Error("src/vendor/** must not be treated as covering src/**")
	}
	whole := overlaps(t,
		paths.Scope{Include: []string{"src/**"}, Exclude: []string{"src/vendor/**"}},
		scope("src/vendor/**"),
	)
	if whole.Overlap {
		t.Error("an exclude that covers the other side should let both proceed")
	}
	partial := overlaps(t,
		paths.Scope{Include: []string{"src/**"}, Exclude: []string{"src/vendor/one.ts"}},
		scope("src/vendor/**"),
	)
	if !partial.Overlap {
		t.Error("a partial exclude must not cancel the conflict")
	}
}

// --------------------------------------------------------------- 2. identity

func TestAMutatingCommandNeverInheritsTheOnlyNameOnTheDoor(t *testing.T) {
	root := bootstrap(t, "gh-ident", "alice")
	r := fridge(t, root, []string{"claim", "src/**", "--task", "x", "--json"}, runOpts{Actor: ""})
	if r.Code != 7 {
		t.Fatalf("exit %d, want 7 (E_NO_SESSION): %s", r.Code, r.Stdout)
	}
	if got := r.JSON.Str("error.code"); got != "E_NO_SESSION" {
		t.Fatalf("error code = %q", got)
	}
	if !strings.Contains(r.JSON.Str("error.hint"), "alice") {
		t.Errorf("the hint should still name who is on the door: %q", r.JSON.Str("error.hint"))
	}
	if n := len(liveClaims(t, root, "alice")); n != 0 {
		t.Fatalf("%d cards were claimed on somebody else's behalf", n)
	}
}

func TestAReadOnlyCommandMayStillGuessTheSoleActor(t *testing.T) {
	root := bootstrap(t, "gh-ident-read", "alice")
	for _, cmd := range [][]string{{"status", "--json"}, {"whoami", "--json"}} {
		if c := fridge(t, root, cmd, runOpts{Actor: ""}).Code; c != 0 {
			t.Errorf("%v exited %d, want 0", cmd, c)
		}
	}
}

// ------------------------------------------------------------- 5. corruption

func TestAnUnreadableCardBlocksANewClaimInsteadOfLookingLikeFreeSpace(t *testing.T) {
	root := bootstrap(t, "gh-corrupt", "alice", "bob")
	id := claimIDOf(t, fridge(t, root, []string{"claim", "src/**", "--task", "x", "--json"}, runOpts{Actor: "alice"}))
	if err := os.WriteFile(filepath.Join(root, ".fridge", "claims", id+".json"), []byte(`{"schema":"wcp/0.1/claim", trunc`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := fridge(t, root, []string{"claim", "src/**", "--task", "steal", "--json"}, runOpts{Actor: "bob"})
	if r.Code != 5 {
		t.Fatalf("a damaged record must fail closed: exit %d, %s", r.Code, r.Stdout)
	}
	if got := r.JSON.Str("error.code"); got != "E_STATE_CORRUPT" {
		t.Fatalf("error code = %q", got)
	}
}

func TestTheBoardStillReadsButSaysLoudlyThatItIsIncomplete(t *testing.T) {
	root := bootstrap(t, "gh-corrupt-view", "alice")
	id := claimIDOf(t, fridge(t, root, []string{"claim", "src/**", "--task", "x", "--json"}, runOpts{Actor: "alice"}))
	if err := os.WriteFile(filepath.Join(root, ".fridge", "claims", id+".json"), []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := fridge(t, root, []string{"status"}, runOpts{Actor: "alice"})
	if r.Code != 0 {
		t.Fatalf("status exited %d, want 0", r.Code)
	}
	if !strings.Contains(strings.ToLower(r.Stdout), "unreadable") {
		t.Fatalf("a corrupt card was silently dropped from the view:\n%s", r.Stdout)
	}
	if c := fridge(t, root, []string{"doctor", "--check"}, runOpts{Actor: "alice"}).Code; c != 30 {
		t.Fatalf("doctor --check exited %d, want 30", c)
	}
}

func TestCorruptClaimsBlockLeaseAndOwnershipMutations(t *testing.T) {
	root := bootstrap(t, "gh-corrupt-mutations", "alice")
	id := claimIDOf(t, fridge(t, root, []string{"claim", "src/**", "--task", "x", "--json"}, runOpts{Actor: "alice"}))
	if err := os.WriteFile(filepath.Join(root, ".fridge", "claims", id+".json"), []byte(`{"schema":"wcp/0.1/claim", trunc`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"heartbeat", "--json"},
		{"release", "--all", "--json"},
		{"reap", "--dry-run", "--json"},
	} {
		result := fridge(t, root, args, runOpts{Actor: "alice"})
		if result.Code != 5 || result.JSON.Str("error.code") != "E_STATE_CORRUPT" {
			t.Fatalf("%s ignored a corrupt claim: exit %d, %s", args[0], result.Code, result.Stdout)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".fridge", "leases", id+".json")); err != nil {
		t.Fatalf("failed mutation changed lease state: %v", err)
	}
}

// --------------------------------------------------------------- 6. handoffs

func TestAnOfferThatWasSupersededCannotBeRedeemedLater(t *testing.T) {
	root := bootstrap(t, "gh-offer", "alice", "bob", "carol")
	id := claimIDOf(t, fridge(t, root, []string{"claim", "src/**", "--task", "x", "--json"}, runOpts{Actor: "alice"}))
	first := fridge(t, root, []string{"handoff", id, "--to", "bob", "--note", "yours", "--json"}, runOpts{Actor: "alice"}).JSON.Str("data.messageId")
	fridge(t, root, []string{"handoff", id, "--to", "carol", "--note", "actually yours", "--json"}, runOpts{Actor: "alice"})

	inbox := fridge(t, root, []string{"inbox", "--json"}, runOpts{Actor: "carol"}).JSON.ArrAt("data.messages")
	if len(inbox) != 1 {
		t.Fatalf("carol has %d messages, want 1", len(inbox))
	}
	second, _ := inbox[0].(jsonx.Obj)["id"].(string)
	if c := fridge(t, root, []string{"accept", second, "--json"}, runOpts{Actor: "carol"}).Code; c != 0 {
		t.Fatalf("carol could not accept a live offer: exit %d", c)
	}

	// First line of defence: superseding withdrew the offer from bob's inbox.
	if c := fridge(t, root, []string{"accept", first, "--json"}, runOpts{Actor: "bob"}).Code; c != 11 {
		t.Fatalf("a withdrawn offer should be gone: exit %d", c)
	}

	// Second line: even a restored envelope must be refused, because the card
	// it names no longer belongs to the agent who offered it.
	env := readObj(t, filepath.Join(root, ".fridge", "archive", "messages", first+".json"))
	env["state"] = "offered"
	if err := os.WriteFile(filepath.Join(root, ".fridge", "inbox", "bob", first+".json"), []byte(jsonx.Compact(env)), 0o644); err != nil {
		t.Fatal(err)
	}
	late := fridge(t, root, []string{"accept", first, "--json"}, runOpts{Actor: "bob"})
	if late.Code != 10 || late.JSON.Str("error.code") != "E_CONFLICT" {
		t.Fatalf("a stale offer moved the card a second time: exit %d, %s", late.Code, late.Stdout)
	}

	live := liveClaims(t, root, "alice")
	if len(live) != 1 {
		t.Fatalf("%d live cards, want 1", len(live))
	}
	if got, _ := live[0].(jsonx.Obj)["actorName"].(string); got != "carol" {
		t.Fatalf("the card ended up with %q, want carol", got)
	}
}

func TestAWithdrawnOfferLeavesTheInboxAndKeepsItsOutcomeOnRecord(t *testing.T) {
	root := bootstrap(t, "gh-withdraw", "alice", "bob", "carol")
	id := claimIDOf(t, fridge(t, root, []string{"claim", "src/**", "--task", "x", "--json"}, runOpts{Actor: "alice"}))
	first := fridge(t, root, []string{"handoff", id, "--to", "bob", "--json"}, runOpts{Actor: "alice"}).JSON.Str("data.messageId")
	fridge(t, root, []string{"handoff", id, "--to", "carol", "--json"}, runOpts{Actor: "alice"})

	if n := len(fridge(t, root, []string{"inbox", "--json"}, runOpts{Actor: "bob"}).JSON.ArrAt("data.messages")); n != 0 {
		t.Fatalf("bob still has %d superseded messages", n)
	}
	if got := readObj(t, filepath.Join(root, ".fridge", "archive", "messages", first+".json")).Str("state"); got != "withdrawn" {
		t.Fatalf("archived state = %q, want withdrawn", got)
	}
}

// ------------------------------------------------------------ 4. wire format

func TestForceReleasingSomebodyElsesCardIsRecordedAsRevoked(t *testing.T) {
	root := bootstrap(t, "gh-revoke", "alice", "bob")
	id := claimIDOf(t, fridge(t, root, []string{"claim", "src/**", "--task", "x", "--json"}, runOpts{Actor: "alice"}))
	if c := fridge(t, root, []string{"release", id, "--force", "--json"}, runOpts{Actor: "bob"}).Code; c != 0 {
		t.Fatalf("force release exited %d", c)
	}
	if got := readObj(t, filepath.Join(root, ".fridge", "archive", "claims", id+".json")).Str("state"); got != "revoked" {
		t.Fatalf("archived state = %q, want revoked", got)
	}
}

func TestReleasingYourOwnCardIsRecordedAsReleased(t *testing.T) {
	root := bootstrap(t, "gh-release-own", "alice")
	id := claimIDOf(t, fridge(t, root, []string{"claim", "src/**", "--task", "x", "--json"}, runOpts{Actor: "alice"}))
	fridge(t, root, []string{"release", id, "--outcome", "done", "--json"}, runOpts{Actor: "alice"})
	if got := readObj(t, filepath.Join(root, ".fridge", "archive", "claims", id+".json")).Str("state"); got != "released" {
		t.Fatalf("archived state = %q, want released", got)
	}
}

// -------------------------------------------------------------- 7. atomicity

func TestANoteIsNeverVisibleAtItsFinalPathBeforeItIsComplete(t *testing.T) {
	root := bootstrap(t, "gh-atomic", "alice")
	fridge(t, root, []string{"pin", "a durable finding about the parser", "--json"}, runOpts{Actor: "alice"})
	for _, f := range noteFiles(t, root) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) == 0 {
			t.Fatalf("%s was published empty", f)
		}
		var probe any
		if err := json.Unmarshal(b, &probe); err != nil {
			t.Fatalf("%s was published half-written: %v", f, err)
		}
	}
}

// ------------------------------------------------------------------ 8. paths

func TestAGeneratedViewCannotBeWrittenOutsideTheWorkspace(t *testing.T) {
	root := bootstrap(t, "gh-escape", "alice")
	for _, target := range []string{"../escaped.md", filepath.Join(root, "..", "escaped.md")} {
		r := fridge(t, root, []string{"render", "--output", target, "--json"}, runOpts{Actor: "alice"})
		if r.Code != 40 {
			t.Fatalf("%s was accepted: exit %d", target, r.Code)
		}
		if got := r.JSON.Str("error.code"); got != "E_PATH_INVALID" {
			t.Fatalf("error code = %q", got)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "..", "escaped.md")); err == nil {
		t.Fatal("a view escaped the workspace")
	}
}

func TestDoorPathIsConfinedAndDrivesAutomaticRendering(t *testing.T) {
	root := bootstrap(t, "gh-door-path", "alice")
	escaped := fridge(t, root, []string{"config", "door.path", "../escaped-door.md", "--json"}, runOpts{Actor: "alice"})
	if escaped.Code != 40 || escaped.JSON.Str("error.code") != "E_PATH_INVALID" {
		t.Fatalf("escaping door.path was accepted: exit %d, %s", escaped.Code, escaped.Stdout)
	}
	if message := escaped.JSON.Str("error.message"); !strings.Contains(message, "door.path") || strings.Contains(message, "[object Object]") {
		t.Fatalf("door.path error label is not human-readable: %q", message)
	}
	if c := fridge(t, root, []string{"config", "door.path", "docs/AGENT-FRIDGE.md", "--json"}, runOpts{Actor: "alice"}).Code; c != 0 {
		t.Fatalf("setting an in-workspace door.path exited %d", c)
	}
	if err := os.Remove(filepath.Join(root, ".fridge", "DOOR.md")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if c := fridge(t, root, []string{"pin", "render the configured door", "--json"}, runOpts{Actor: "alice"}).Code; c != 0 {
		t.Fatalf("pin exited %d", c)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "AGENT-FRIDGE.md")); err != nil {
		t.Fatalf("configured door was not rendered: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".fridge", "DOOR.md")); !os.IsNotExist(err) {
		t.Fatalf("auto-render ignored door.path: %v", err)
	}
}

func TestMalformedDoorConfigurationIsRejectedBeforeItCanRedirectAView(t *testing.T) {
	root := bootstrap(t, "gh-door-shape", "alice")
	configPath := filepath.Join(root, ".fridge", "config.json")
	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	originalDoor := original["door"].(map[string]any)
	cases := []map[string]any{
		{"path": float64(42), "autoRender": true, "extraTargets": []any{}},
		{"path": "", "autoRender": true, "extraTargets": []any{}},
		{"path": ".fridge/DOOR.md", "autoRender": true, "extraTargets": "docs/door.md"},
		{"path": ".fridge/DOOR.md", "autoRender": true, "extraTargets": []any{"docs/door.md", float64(42)}},
	}
	for _, door := range cases {
		original["door"] = door
		body, _ := json.MarshalIndent(original, "", "  ")
		if err := os.WriteFile(configPath, append(body, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		result := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "alice"})
		if result.Code != 5 || result.JSON.Str("error.code") != "E_STATE_CORRUPT" {
			t.Fatalf("malformed door config was accepted: exit %d, %s", result.Code, result.Stdout)
		}
	}
	original["door"] = originalDoor
	body, _ := json.MarshalIndent(original, "", "  ")
	if err := os.WriteFile(configPath, append(body, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	invalid := fridge(t, root, []string{"config", "door.extraTargets", "docs/door.md", "--json"}, runOpts{Actor: "alice"})
	if invalid.Code != 2 || invalid.JSON.Str("error.code") != "E_USAGE" {
		t.Fatalf("non-array extraTargets was accepted: exit %d, %s", invalid.Code, invalid.Stdout)
	}
	if result := fridge(t, root, []string{"config", "door.extraTargets", "[\"docs/door.md\"]", "--json"}, runOpts{Actor: "alice"}); result.Code != 0 {
		t.Fatalf("valid extraTargets array exited %d: %s", result.Code, result.Stdout)
	}
	loaded, ok := fsx.ReadJSONSafe(configPath)
	targets := loaded.Strings("door.extraTargets")
	if !ok || len(targets) != 1 || targets[0] != "docs/door.md" {
		t.Fatalf("valid extraTargets array was not stored: %v", loaded.Get("door.extraTargets"))
	}
}

func TestASymlinkedOutputPathIsJudgedByWhereItReallyLands(t *testing.T) {
	root := bootstrap(t, "gh-symlink", "alice")
	outside := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outside)
	if err := os.Symlink(outside, filepath.Join(root, "away")); err != nil {
		t.Skipf("no symlink privilege here: %v", err)
	}
	if c := fridge(t, root, []string{"render", "--output", "away/door.md", "--json"}, runOpts{Actor: "alice"}).Code; c != 40 {
		t.Fatalf("a symlinked escape exited %d, want 40", c)
	}
	if _, err := os.Stat(filepath.Join(outside, "door.md")); err == nil {
		t.Fatal("a view was written through a symlink out of the workspace")
	}
}

func TestASimulationReportCannotBeWrittenOutsideTheWorkspace(t *testing.T) {
	root := bootstrap(t, "gh-report", "alice")
	if c := fridge(t, root, []string{"simulate", "--agents", "2", "--report", "../report.md", "--json"}, runOpts{Actor: "alice"}).Code; c != 40 {
		t.Fatalf("simulate --report escaped: exit %d", c)
	}
	if _, err := os.Stat(filepath.Join(root, "..", "report.md")); err == nil {
		t.Fatal("a report escaped the workspace")
	}
}

// ---------------------------------------------------------------- 9. secrets

func TestEveryDurableFreeTextFieldIsScannedNotJustTheNoteBody(t *testing.T) {
	root := bootstrap(t, "gh-secret", "alice")
	token := "ghp_" + "A1b2C3d4E5f6G7h8I9j0"
	for _, args := range [][]string{
		{"claim", "src/**", "--task", "deploy with " + token},
		{"pin", "ordinary text", "--task", "deploy with " + token},
	} {
		r := fridge(t, root, append(args, "--json"), runOpts{Actor: "alice"})
		if r.Code != 2 {
			t.Fatalf("%s accepted a token: exit %d", args[0], r.Code)
		}
		if got := r.JSON.Str("error.code"); got != "E_USAGE" {
			t.Fatalf("error code = %q", got)
		}
	}
	id := claimIDOf(t, fridge(t, root, []string{"claim", "src/**", "--task", "clean", "--json"}, runOpts{Actor: "alice"}))
	if c := fridge(t, root, []string{"release", id, "--note", "oops " + token, "--json"}, runOpts{Actor: "alice"}).Code; c != 2 {
		t.Fatalf("a release note is durable too: exit %d", c)
	}
	for _, n := range notes(t, root) {
		if strings.Contains(jsonx.Compact(n), token) {
			t.Fatal("a token reached the wall")
		}
	}
}

func TestTheSecretEscapeHatchNamesTheOffendingFlag(t *testing.T) {
	token := "ghp_" + "A1b2C3d4E5f6G7h8I9j0"
	if secrets.Looks(token) == "" {
		t.Fatal("a GitHub token was not recognised")
	}
	if err := secrets.Guard(map[string]string{"--task": token}, true); err != nil {
		t.Fatalf("the escape hatch should allow it: %v", err)
	}
	err := secrets.Guard(map[string]string{"--task": token}, false)
	if err == nil || !strings.Contains(err.Error(), "--task") {
		t.Fatalf("the error should name the flag, got %v", err)
	}
}

func TestSoleActorInferenceNeverRenewsOrCreatesAMisspelledActor(t *testing.T) {
	root := bootstrap(t, "gh-ident-read-only", "alice")
	typo := fridge(t, root, []string{"status", "--agent", "alcie", "--json"}, runOpts{})
	if typo.Code != 7 {
		t.Fatalf("misspelled actor exited %d, want 7", typo.Code)
	}
	if _, err := os.Stat(filepath.Join(root, ".fridge", "actors", "alcie.json")); !os.IsNotExist(err) {
		t.Fatalf("a read typo silently joined: %v", err)
	}

	held := fridge(t, root, []string{"claim", "src/**", "--task", "near expiry", "--json"}, runOpts{Actor: "alice"})
	leaseFile := filepath.Join(root, ".fridge", "leases", claimIDOf(t, held)+".json")
	lease := readObj(t, leaseFile)
	lease["expiresAt"] = util.NowISO(time.Now().Add(5 * time.Second))
	lease["renewals"] = float64(0)
	if err := os.WriteFile(leaseFile, []byte(jsonx.Compact(lease)), 0o644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(leaseFile)
	if c := fridge(t, root, []string{"status", "--json"}, runOpts{}).Code; c != 0 {
		t.Fatalf("inferred status exited %d", c)
	}
	after, _ := os.ReadFile(leaseFile)
	if string(after) != string(before) {
		t.Fatal("inferred read-only identity renewed a lease")
	}
}

func TestAStaleEnvironmentIdentityDoesNotBlockIdentityFreeAdministration(t *testing.T) {
	root := bootstrap(t, "gh-ident-stale-env", "alice")
	for _, args := range [][]string{
		{"status", "--json"},
		{"board", "--json"},
		{"config", "--json"},
		{"doctor", "--check", "--json"},
	} {
		result := fridge(t, root, args, runOpts{Actor: "departed"})
		if result.Code != 0 {
			t.Fatalf("%s was blocked by a stale FRIDGE_ACTOR: exit %d, stdout %s, stderr %s",
				args[0], result.Code, result.Stdout, result.Stderr)
		}
	}
	held := claimIDOf(t, fridge(t, root, []string{"claim", "src/**", "--task", "held", "--json"}, runOpts{Actor: "alice"}))
	if result := fridge(t, root, []string{"reap", "--dry-run", "--json"}, runOpts{Actor: "departed"}); result.Code != 0 {
		t.Fatalf("reap --dry-run was blocked by stale FRIDGE_ACTOR: %d, %s", result.Code, result.Stdout)
	}
	if result := fridge(t, root, []string{"wait", held, "--timeout", "1ms", "--json"}, runOpts{Actor: "departed"}); result.Code != 21 {
		t.Fatalf("wait was blocked by stale FRIDGE_ACTOR: %d, %s", result.Code, result.Stdout)
	}
	if result := fridge(t, root, []string{"reap", "--dry-run", "--agent", "departed", "--json"}, runOpts{}); result.Code != 7 {
		t.Fatalf("explicit stale --agent exited %d, want 7", result.Code)
	}
}

func TestMigrationIsExplicitConfinedAndSecretScannedBeforeWriting(t *testing.T) {
	root := bootstrap(t, "gh-migrate-safety", "alice")
	legacy := filepath.Join(root, "shared-development-updates.md")
	outside := filepath.Join(filepath.Dir(root), fmt.Sprintf("outside-ledger-%d.md", os.Getpid()))
	if err := os.WriteFile(legacy, []byte("- ghp_abcdefghijklmnopqrstuvwxyz1234567890\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("- harmless outside line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)
	before := len(notes(t, root))

	inferred := fridge(t, root, []string{"migrate", "--updates", "shared-development-updates.md", "--json"}, runOpts{})
	if inferred.Code != 7 {
		t.Fatalf("non-dry migration inherited the sole actor: exit %d, %s", inferred.Code, inferred.Stdout)
	}
	if len(notes(t, root)) != before {
		t.Fatal("identity failure wrote migration notes")
	}

	dry := fridge(t, root, []string{"migrate", "--updates", "shared-development-updates.md", "--dry-run", "--json"}, runOpts{})
	if dry.Code != 2 {
		t.Fatalf("dry-run did not report the secret that blocks import: exit %d", dry.Code)
	}

	blocked := fridge(t, root, []string{
		"migrate", "--updates", "shared-development-updates.md", "--freeze", "--json",
	}, runOpts{Actor: "alice"})
	if blocked.Code != 2 || blocked.JSON.Str("error.code") != "E_USAGE" {
		t.Fatalf("secret migration was accepted: exit %d, %s", blocked.Code, blocked.Stdout)
	}
	if len(notes(t, root)) != before {
		t.Fatal("migration wrote notes before validation finished")
	}
	raw, _ := os.ReadFile(legacy)
	if strings.HasPrefix(string(raw), "<!-- FROZEN") {
		t.Fatal("migration froze the source before validation passed")
	}

	escaped := fridge(t, root, []string{
		"migrate", "--updates", "../" + filepath.Base(outside), "--json",
	}, runOpts{Actor: "alice"})
	if escaped.Code != 40 || escaped.JSON.Str("error.code") != "E_PATH_INVALID" {
		t.Fatalf("outside migration path was accepted: exit %d, %s", escaped.Code, escaped.Stdout)
	}

	emptyDone := filepath.Join(root, "To-do.done.md")
	if err := os.WriteFile(emptyDone, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	preview := fridge(t, root, []string{
		"migrate",
		"--updates", "shared-development-updates.md",
		"--todo-done", "To-do.done.md",
		"--allow-secret-like",
		"--dry-run",
		"--json",
	}, runOpts{Actor: "alice"})
	if preview.Code != 0 {
		t.Fatalf("migration preview exited %d: %s", preview.Code, preview.Stdout)
	}
	gotFiles := map[string]bool{}
	for _, rawFile := range preview.JSON.ArrAt("data.files") {
		file, _ := rawFile.(string)
		gotFiles[file] = true
	}
	for _, name := range []string{"shared-development-updates.md", "To-do.done.md"} {
		if !gotFiles[name] {
			t.Fatalf("zero-entry migration source %q was omitted: %s", name, preview.Stdout)
		}
	}

	allowed := fridge(t, root, []string{
		"migrate", "--updates", "shared-development-updates.md", "--allow-secret-like", "--json",
	}, runOpts{Actor: "alice"})
	if allowed.Code != 0 {
		t.Fatalf("allow-secret-like did not override scanner: exit %d, %s", allowed.Code, allowed.Stdout)
	}
	imported := 0
	for _, note := range notes(t, root) {
		if note.Str("type") == "legacy.update" {
			imported++
		}
	}
	if imported != 1 {
		t.Fatalf("imported %d legacy notes, want 1", imported)
	}
}

func TestMigrationRefusesToFreezeASourceThatChangedAfterPreflight(t *testing.T) {
	root := bootstrap(t, "gh-migrate-edited", "alice")
	legacy := filepath.Join(root, "shared-development-updates.md")
	if err := os.WriteFile(legacy, []byte("- original entry\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := len(notes(t, root))
	cmd := exec.Command(binary(t),
		"migrate", "--updates", "shared-development-updates.md", "--freeze", "--json")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"FRIDGE_ACTOR=alice",
		"FRIDGE_TEST=1",
		"FRIDGE_FAULT=delay-migrate-after-preflight",
		"NO_COLOR=1",
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, ".fridge", "tmp", "migrate-preflight-ready")
	deadline := time.Now().Add(5 * time.Second)
	for !fsx.Exists(marker) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !fsx.Exists(marker) {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("migration never reached its preflight seam: %s", output.String())
	}
	file, err := os.OpenFile(legacy, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("- concurrent edit\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".fridge", "tmp", "migrate-preflight-continue"), []byte("continue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	exit := 0
	if err != nil {
		if exited, ok := err.(*exec.ExitError); ok {
			exit = exited.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if exit != 10 {
		t.Fatalf("changed-source migration exited %d, want 10: %s", exit, output.String())
	}
	raw, err := os.ReadFile(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "concurrent edit") || strings.HasPrefix(string(raw), "<!-- FROZEN") {
		t.Fatalf("migration overwrote or froze concurrent edits:\n%s", raw)
	}
	if len(notes(t, root)) != before {
		t.Fatal("migration wrote notes before detecting the changed source")
	}
}

func TestMigrationRejectsASourceLargerThanItsBoundedMutexBudget(t *testing.T) {
	root := bootstrap(t, "gh-migrate-size", "alice")
	legacy := filepath.Join(root, "shared-development-updates.md")
	if err := os.WriteFile(legacy, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(legacy, 10*1024*1024+1); err != nil {
		t.Fatal(err)
	}
	result := fridge(t, root, []string{
		"migrate", "--updates", "shared-development-updates.md", "--dry-run", "--json",
	}, runOpts{})
	if result.Code != 2 || !strings.Contains(result.JSON.Str("error.message"), "10 MiB") {
		t.Fatalf("oversized migration source was accepted: exit %d, %s", result.Code, result.Stdout)
	}
}

// ----------------------------------------------------------- 10. lost updates

func TestTwoConfigWritesBothSurvive(t *testing.T) {
	root := bootstrap(t, "gh-config", "alice")
	fridge(t, root, []string{"config", "lease.defaultTtlMs", "600000"}, runOpts{Actor: "alice"})
	fridge(t, root, []string{"config", "door.autoRender", "false"}, runOpts{Actor: "alice"})
	cfg := readObj(t, filepath.Join(root, ".fridge", "config.json"))
	if got := cfg.Int("lease.defaultTtlMs"); got != 600000 {
		t.Errorf("lease.defaultTtlMs = %d, want 600000", got)
	}
	if cfg.Bool("door.autoRender") {
		t.Error("door.autoRender was lost")
	}
}

// --------------------------------------------------------------- 11. renewal

func TestRenewalIsCentralisedSoACommandThatNeverRenewedBeforeDoesNow(t *testing.T) {
	root := bootstrap(t, "gh-renew", "alice")
	fridge(t, root, []string{"config", "lease.renewThresholdRatio", "1.5"}, runOpts{Actor: "alice"})
	id := claimIDOf(t, fridge(t, root, []string{"claim", "src/**", "--task", "x", "--ttl", "30s", "--json"}, runOpts{Actor: "alice"}))
	lease := filepath.Join(root, ".fridge", "leases", id+".json")
	before := readObj(t, lease).Int("renewals")
	fridge(t, root, []string{"inbox", "--json"}, runOpts{Actor: "alice"})
	if after := readObj(t, lease).Int("renewals"); after <= before {
		t.Fatalf("inbox did not renew the lease: %d -> %d", before, after)
	}
}

// ---------------------------------------------------------- 13. notes.commit

func TestNotesCommitFalseKeepsTheNotesWallOutOfGit(t *testing.T) {
	root := bootstrap(t, "gh-notes-commit", "alice")
	fridge(t, root, []string{"config", "notes.commit", "false"}, runOpts{Actor: "alice"})
	b, err := os.ReadFile(filepath.Join(root, ".fridge", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "!/notes/") {
		t.Fatal("the ignore file still un-ignores notes")
	}
	if string(b) != store.GitignoreFor(false) {
		t.Fatal("the ignore file does not match the generator")
	}
	if c := fridge(t, root, []string{"doctor", "--check"}, runOpts{Actor: "alice"}).Code; c != 0 {
		t.Fatalf("doctor --check exited %d, want 0", c)
	}
}

func TestDoctorRepairsAnIgnoreFileThatDisagreesWithNotesCommit(t *testing.T) {
	root := bootstrap(t, "gh-notes-drift", "alice")
	if err := os.WriteFile(filepath.Join(root, ".fridge", ".gitignore"), []byte("/*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := fridge(t, root, []string{"doctor", "--check"}, runOpts{Actor: "alice"}).Code; c != 30 {
		t.Fatalf("doctor --check exited %d, want 30", c)
	}
	if c := fridge(t, root, []string{"doctor", "--fix"}, runOpts{Actor: "alice"}).Code; c != 0 {
		t.Fatalf("doctor --fix exited %d", c)
	}
	b, _ := os.ReadFile(filepath.Join(root, ".fridge", ".gitignore"))
	if string(b) != store.GitignoreFor(true) {
		t.Fatal("doctor --fix did not restore the generated ignore file")
	}
}

// ------------------------------------------------- 4. lock events on record

func TestBreakingAnAbandonedLockIsNeverSilent(t *testing.T) {
	root := bootstrap(t, "gh-lock-note", "alice")
	lockDir := filepath.Join(root, ".fridge", "locks", "registry.lock.d")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := jsonx.Obj{
		"pid": float64(999999), "host": "a-machine-that-is-not-this-one", "op": "claim",
		"acquiredAt": util.NowISO(time.Now().Add(-time.Hour)),
	}
	if err := os.WriteFile(filepath.Join(lockDir, "owner.json"), []byte(jsonx.Compact(owner)), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := fridge(t, root, []string{"claim", "src/**", "--task", "take over", "--json"}, runOpts{Actor: "alice"}).Code; c != 0 {
		t.Fatalf("could not take over an abandoned lock: exit %d", c)
	}
	found := false
	for _, n := range notes(t, root) {
		if n.Str("type") != "lock.broken" {
			continue
		}
		found = true
		if got := n.Str("data.why"); got != "owner-stale" {
			t.Errorf("why = %q, want owner-stale", got)
		}
		if got := n.Int("data.previousOwner.pid"); got != 999999 {
			t.Errorf("previousOwner.pid = %d, want 999999", got)
		}
	}
	if !found {
		t.Fatal("breaking a lock left no record")
	}
}

// ------------------------------------------------------------ 14. one snapshot

func TestTheDoorBodyAndItsStateStampDescribeTheSameInstant(t *testing.T) {
	root := bootstrap(t, "gh-snapshot", "alice")
	fridge(t, root, []string{"config", "door.autoRender", "false"}, runOpts{Actor: "alice"})
	fridge(t, root, []string{"claim", "src/**", "--task", "x", "--json"}, runOpts{Actor: "alice"})
	fridge(t, root, []string{"render", "--json"}, runOpts{Actor: "alice"})
	if c := fridge(t, root, []string{"render", "--check", "--json"}, runOpts{Actor: "alice"}).Code; c != 0 {
		t.Fatalf("a freshly rendered door reported drift: exit %d", c)
	}
	fridge(t, root, []string{"claim", "docs/**", "--task", "y", "--json"}, runOpts{Actor: "alice"})
	if c := fridge(t, root, []string{"render", "--check", "--json"}, runOpts{Actor: "alice"}).Code; c != 30 {
		t.Fatalf("drift must be visible: exit %d", c)
	}
	fridge(t, root, []string{"render", "--json"}, runOpts{Actor: "alice"})
	b, _ := os.ReadFile(filepath.Join(root, ".fridge", "DOOR.md"))
	if !strings.Contains(string(b), "docs/**") {
		t.Fatal("the door body did not catch up")
	}
	if c := fridge(t, root, []string{"render", "--check", "--json"}, runOpts{Actor: "alice"}).Code; c != 0 {
		t.Fatalf("the stamp did not catch up: exit %d", c)
	}
}
