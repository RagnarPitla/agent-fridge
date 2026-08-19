// SPDX-License-Identifier: Apache-2.0
// Genuine OS-level contention: N separate processes reach for the same chore at
// the same agreed instant. Every child is a real process running the real code
// path, released by a filesystem barrier so what is measured is the lock and
// not process startup. Assertions are invariants, never wall-clock durations.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/commands"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/paths"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

// waitForBarrier blocks until the agreed start instant, so every child leaves
// the gate together.
func waitForBarrier(file string) {
	if file == "" {
		return
	}
	var goAt int64
	for {
		b, err := os.ReadFile(file)
		if err == nil {
			if n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil {
				goAt = n
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	for util.NowMs() < goAt {
		// busy-wait to the agreed instant
	}
}

// TestRaceHelperProcess is re-executed as each racing child. Skipped during a
// normal test run.
func TestRaceHelperProcess(t *testing.T) {
	mode := os.Getenv("FRIDGE_RACE_MODE")
	if mode == "" {
		t.Skip("not a race child")
	}
	actor := os.Getenv("FRIDGE_ACTOR")
	waitForBarrier(os.Getenv("FRIDGE_BARRIER"))

	switch mode {
	case "claim":
		ttl := os.Getenv("RACE_TTL")
		if ttl == "" {
			ttl = "30s"
		}
		claimMode := os.Getenv("RACE_MODE")
		if claimMode == "" {
			claimMode = "exclusive"
		}
		var out bytes.Buffer
		code := commands.Main([]string{
			"claim", os.Getenv("RACE_TARGET"), "--task", "race " + actor,
			"--mode", claimMode, "--ttl", ttl, "--json",
		}, &out, io.Discard)
		payload, _ := jsonx.ParseObj(out.Bytes())
		var claimID, errCode any
		if payload != nil {
			if v := payload.Str("data.claimId"); v != "" {
				claimID = v
			}
			if v := payload.Str("error.code"); v != "" {
				errCode = v
			}
		}
		report(jsonx.Obj{"actor": actor, "code": float64(code), "claimId": claimID, "error": errCode})

	case "config":
		var out bytes.Buffer
		code := commands.Main([]string{"config", os.Getenv("RACE_KEY"), os.Getenv("RACE_VALUE"), "--json"}, &out, io.Discard)
		payload, _ := jsonx.ParseObj(out.Bytes())
		var errCode any
		if payload != nil && payload.Str("error.code") != "" {
			errCode = payload.Str("error.code")
		}
		report(jsonx.Obj{"code": float64(code), "key": os.Getenv("RACE_KEY"), "error": errCode})

	case "heartbeat":
		var out bytes.Buffer
		code := commands.Main([]string{"heartbeat", "--json"}, &out, io.Discard)
		payload, _ := jsonx.ParseObj(out.Bytes())
		var errCode any
		if payload != nil && payload.Str("error.code") != "" {
			errCode = payload.Str("error.code")
		}
		report(jsonx.Obj{"code": float64(code), "renewed": float64(len(payload.ArrAt("data.renewed"))), "error": errCode})

	case "write-fridge":
		count := envCount("WRITE_COUNT", 25)
		written := 0
		for i := 0; i < count; i++ {
			if commands.Main([]string{"pin", fmt.Sprintf("%s line %d", actor, i), "--quiet"}, io.Discard, io.Discard) == 0 {
				written++
			}
		}
		report(jsonx.Obj{"actor": actor, "written": float64(written)})

	case "write-shared":
		count := envCount("WRITE_COUNT", 25)
		shared := os.Getenv("SHARED_FILE")
		written := 0
		for i := 0; i < count; i++ {
			// Deliberately the naive read-modify-write every agent reaches for first.
			current, _ := os.ReadFile(shared)
			next := string(current) + fmt.Sprintf("- %s line %d\n", actor, i)
			if err := os.WriteFile(shared, []byte(next), 0o644); err == nil {
				written++
			}
		}
		report(jsonx.Obj{"actor": actor, "written": float64(written)})

	case "crash":
		ttl := os.Getenv("CRASH_TTL")
		if ttl == "" {
			ttl = "2s"
		}
		commands.Main([]string{"claim", os.Getenv("CRASH_TARGET"), "--task", "about to crash", "--ttl", ttl, "--json"},
			io.Discard, io.Discard)
		if os.Getenv("CRASH_MODE") == "mutex" {
			// Simulate a process killed while holding the registry lock.
			cwd, _ := os.Getwd()
			dir := filepath.Join(cwd, ".fridge", "locks", "registry.lock.d")
			os.MkdirAll(dir, 0o755)
			owner := jsonx.Obj{
				"pid": float64(999999), "host": "someone-else", "op": "claim",
				"acquiredAt": util.NowISO(time.Now().Add(-time.Hour)),
			}
			os.WriteFile(filepath.Join(dir, "owner.json"), []byte(jsonx.Compact(owner)), 0o644)
		}
		hardKillSelf()
	}
	os.Exit(0)
}

func envCount(name string, fallback int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func report(v jsonx.Obj) {
	fmt.Fprintf(os.Stdout, "%s\n", jsonx.Compact(v))
	os.Exit(0)
}

type raceResult struct {
	Env    map[string]string
	Code   int
	Report jsonx.Obj
	Stdout string
	Stderr string
}

// race starts one child per environment, releases them all at once, and
// collects what each one reported.
func race(t *testing.T, root string, perChild []map[string]string) []raceResult {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(repoRoot(t), ".scratch", "gotest", "barriers")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	barrier := filepath.Join(base, fmt.Sprintf("barrier-%d-%s", os.Getpid(), strconv36(counter.Add(1))))
	defer os.Remove(barrier)

	results := make([]raceResult, len(perChild))
	var wg sync.WaitGroup
	for i, env := range perChild {
		wg.Add(1)
		go func(i int, env map[string]string) {
			defer wg.Done()
			args := []string{"-test.run=^TestRaceHelperProcess$"}
			cmdEnv := append([]string{}, os.Environ()...)
			cmdEnv = append(cmdEnv, "FRIDGE_BARRIER="+barrier, "NO_COLOR=1")
			for k, v := range env {
				cmdEnv = append(cmdEnv, k+"="+v)
			}
			out, errOut, code := runChild(self, args, root, cmdEnv)
			var rep jsonx.Obj
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if v, err := jsonx.ParseObj([]byte(line)); err == nil {
					rep = v
				}
			}
			results[i] = raceResult{Env: env, Code: code, Report: rep, Stdout: out, Stderr: errOut}
		}(i, env)
	}
	// Give every child time to reach the barrier before the agreed start instant.
	time.Sleep(250 * time.Millisecond)
	if err := os.WriteFile(barrier, []byte(strconv.FormatInt(util.NowMs()+60, 10)), 0o644); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	return results
}

func actorNames(n int, prefix string) []string {
	out := []string{}
	for i := 0; i < n; i++ {
		out = append(out, fmt.Sprintf("%s-%02d", prefix, i))
	}
	return out
}

func withCode(results []raceResult, code int) []raceResult {
	out := []raceResult{}
	for _, r := range results {
		if r.Report != nil && int(r.Report.Num("code")) == code {
			out = append(out, r)
		}
	}
	return out
}

func detail(results []raceResult) string {
	parts := []string{}
	for _, r := range results {
		if r.Report == nil {
			parts = append(parts, "null(stderr="+r.Stderr+")")
			continue
		}
		parts = append(parts, jsonx.Compact(r.Report))
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func readObjFile(t *testing.T, file string) jsonx.Obj {
	t.Helper()
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := jsonx.ParseObj(raw)
	if err != nil {
		t.Fatal(err)
	}
	return obj
}

func TestEightProcessesOneFileExactlyOneWinner(t *testing.T) {
	names := actorNames(8, "agent")
	root := bootstrap(t, "race-same", names...)
	perChild := []map[string]string{}
	for _, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": a, "RACE_TARGET": "src/api/routes.ts",
		})
	}
	results := race(t, root, perChild)
	if len(results) != 8 {
		t.Fatalf("%d results", len(results))
	}
	for _, r := range results {
		if r.Report == nil {
			t.Fatalf("a child crashed instead of reporting: %s", detail(results))
		}
	}
	winners := withCode(results, 0)
	refused := withCode(results, 10)
	if len(winners) != 1 {
		t.Errorf("expected exactly one winner, got %d: %s", len(winners), detail(results))
	}
	if len(refused) != 7 {
		t.Errorf("expected seven E_CONFLICT, got %d: %s", len(refused), detail(results))
	}
	for _, r := range refused {
		if r.Report.Str("error") != "E_CONFLICT" {
			t.Errorf("refusal carried %q", r.Report.Str("error"))
		}
	}
	live := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: names[0]}).JSON.ArrAt("data.claims")
	if len(live) != 1 {
		t.Fatalf("the board shows %d cards", len(live))
	}
	card, _ := live[0].(jsonx.Obj)
	if len(winners) == 1 && card.Str("actorName") != winners[0].Report.Str("actor") {
		t.Errorf("the board disagrees with the winner")
	}
	acquired, denied := 0, 0
	for _, n := range notes(t, root) {
		switch n.Str("type") {
		case "claim.acquired":
			acquired++
		case "claim.denied":
			denied++
		}
	}
	if acquired != 1 || denied != 7 {
		t.Errorf("wall shows %d acquired and %d denied", acquired, denied)
	}
}

func TestRangeAndLiteralExactlyOneWinner(t *testing.T) {
	root := bootstrap(t, "race-range", "left", "right")
	results := race(t, root, []map[string]string{
		{"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": "left", "RACE_TARGET": "[a-z].md"},
		{"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": "right", "RACE_TARGET": "b.md"},
	})
	if len(withCode(results, 0)) != 1 || len(withCode(results, 10)) != 1 {
		t.Fatalf("range and literal must have one winner and one refusal: %s", detail(results))
	}
}

func TestConcurrentConfigWritesPreserveEveryKey(t *testing.T) {
	root := bootstrap(t, "race-config", "alice")
	results := race(t, root, []map[string]string{
		{"FRIDGE_RACE_MODE": "config", "RACE_KEY": "paths.materializeLimit", "RACE_VALUE": "1234"},
		{"FRIDGE_RACE_MODE": "config", "RACE_KEY": "lease.graceMs", "RACE_VALUE": "4321"},
	})
	if len(withCode(results, 0)) != 2 {
		t.Fatalf("config writers failed: %s", detail(results))
	}
	config := readObjFile(t, filepath.Join(root, ".fridge", "config.json"))
	if config.Int("paths.materializeLimit") != 1234 || config.Int("lease.graceMs") != 4321 {
		t.Fatalf("one config write was lost: %s", jsonx.Compact(config))
	}
}

func TestConcurrentSameSessionClaimsPreserveEveryToken(t *testing.T) {
	root := bootstrap(t, "race-session-tokens", "alice")
	perChild := []map[string]string{}
	for i := 0; i < 8; i++ {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": "alice",
			"RACE_TARGET": fmt.Sprintf("parallel/file-%d.ts", i),
		})
	}
	results := race(t, root, perChild)
	if len(withCode(results, 0)) != len(perChild) {
		t.Fatalf("same-session claims failed: %s", detail(results))
	}
	actor := readObjFile(t, filepath.Join(root, ".fridge", "actors", "alice.json"))
	session := readObjFile(t, filepath.Join(root, ".fridge", "sessions", actor.Str("currentSessionId")+".json"))
	tokens := session.ObjAt("tokens")
	if len(tokens) != len(perChild) {
		t.Fatalf("same-session read-modify-write lost tokens: got %d want %d", len(tokens), len(perChild))
	}
	for _, result := range results {
		if tokens.Str(result.Report.Str("claimId")) == "" {
			t.Fatalf("session is missing token for %s", result.Report.Str("claimId"))
		}
	}
	if r := fridge(t, root, []string{"render", "--check", "--json"}, runOpts{Actor: "alice"}); r.Code != 0 {
		t.Fatalf("auto-render left an older snapshot on the door: %s", r.Stdout)
	}
}

func TestConcurrentHeartbeatsIncrementRenewals(t *testing.T) {
	root := bootstrap(t, "race-heartbeat", "alice")
	claimed := fridge(t, root, []string{
		"claim", "src/**", "--task", "heartbeat race", "--ttl", "30s", "--json",
	}, runOpts{Actor: "alice"})
	if claimed.Code != 0 {
		t.Fatalf("claim failed: %s", claimed.Stdout)
	}
	claimID := claimed.JSON.Str("data.claimId")
	perChild := []map[string]string{}
	for i := 0; i < 8; i++ {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "heartbeat", "FRIDGE_ACTOR": "alice", "FRIDGE_NO_RENEW": "1",
		})
	}
	results := race(t, root, perChild)
	if len(withCode(results, 0)) != len(perChild) {
		t.Fatalf("heartbeats failed: %s", detail(results))
	}
	lease := readObjFile(t, filepath.Join(root, ".fridge", "leases", claimID+".json"))
	if lease.Int("renewals") != len(perChild) {
		t.Fatalf("heartbeat writers overwrote one another: got %d want %d", lease.Int("renewals"), len(perChild))
	}
}

// The honest invariant for overlapping-but-not-nested targets is not "one
// winner" - it is "no two granted claims overlap".
func TestOverlappingGlobsWinnersNeverOverlap(t *testing.T) {
	names := actorNames(8, "glob")
	root := bootstrap(t, "race-glob", names...)
	targets := []string{"src/**", "src/api/**", "src/api/routes.ts", "src/api/*.ts", "src/**/*.ts", "src/api", "src/api/db.ts", "src/**"}
	perChild := []map[string]string{}
	for i, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": a, "RACE_TARGET": targets[i],
		})
	}
	results := race(t, root, perChild)
	winners := withCode(results, 0)
	refused := withCode(results, 10)
	if len(results) != 8 {
		t.Fatalf("%d results", len(results))
	}
	if len(winners)+len(refused) != 8 {
		t.Fatalf("every child must either win or be refused: %s", detail(results))
	}
	if len(winners) < 1 {
		t.Fatalf("someone must make progress: %s", detail(results))
	}
	held := []string{}
	for _, w := range winners {
		for i, n := range names {
			if n == w.Report.Str("actor") {
				held = append(held, targets[i])
			}
		}
	}
	for i := 0; i < len(held); i++ {
		for j := i + 1; j < len(held); j++ {
			got, err := paths.ScopesOverlap(
				paths.Scope{Include: []string{held[i]}, Exclude: []string{}},
				paths.Scope{Include: []string{held[j]}, Exclude: []string{}}, false)
			if err != nil {
				t.Fatal(err)
			}
			if got.Overlap {
				t.Errorf("%s and %s were both granted but overlap: %s", held[i], held[j], detail(results))
			}
		}
	}
	live := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: names[0]}).JSON.ArrAt("data.claims")
	if len(live) != len(winners) {
		t.Errorf("the board shows %d cards for %d winners", len(live), len(winners))
	}
}

func TestFullyNestedFamilyCollapsesToOneWinner(t *testing.T) {
	names := actorNames(8, "nest")
	root := bootstrap(t, "race-nested", names...)
	// Every target here contains, or is contained by, every other one.
	targets := []string{"src/**", "src/api/**", "src/api/routes.ts", "src/api/*.ts", "src/**/*.ts", "src/api", "src/api/routes.ts", "src/**"}
	perChild := []map[string]string{}
	for i, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": a, "RACE_TARGET": targets[i],
		})
	}
	results := race(t, root, perChild)
	if n := len(withCode(results, 0)); n != 1 {
		t.Errorf("a fully nested family must collapse to one winner, got %d: %s", n, detail(results))
	}
	if n := len(withCode(results, 10)); n != 7 {
		t.Errorf("expected seven E_CONFLICT, got %d: %s", n, detail(results))
	}
}

func TestEightSeparateChoresNobodyIsBlocked(t *testing.T) {
	names := actorNames(8, "wide")
	root := bootstrap(t, "race-disjoint", names...)
	targets := []string{"src/api/routes.ts", "src/api/db.ts", "src/ui/app.tsx", "docs/guide.md", "README.md", "src/new-a.ts", "src/new-b.ts", "docs/new-c.md"}
	perChild := []map[string]string{}
	for i, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": a, "RACE_TARGET": targets[i],
		})
	}
	results := race(t, root, perChild)
	winners := withCode(results, 0)
	if len(winners) != 8 {
		t.Fatalf("all eight disjoint claims should succeed, got %d: %s", len(winners), detail(results))
	}
	ids := map[string]bool{}
	for _, w := range winners {
		ids[w.Report.Str("claimId")] = true
	}
	if len(ids) != 8 {
		t.Errorf("%d distinct cards, want 8", len(ids))
	}
	if n := len(fridge(t, root, []string{"status", "--json"}, runOpts{Actor: names[0]}).JSON.ArrAt("data.claims")); n != 8 {
		t.Errorf("the board shows %d cards", n)
	}
}

func TestSharedReadersNeverBlockEachOther(t *testing.T) {
	names := actorNames(6, "reader")
	root := bootstrap(t, "race-shared", names...)
	perChild := []map[string]string{}
	for _, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": a, "RACE_TARGET": "docs/**", "RACE_MODE": "shared",
		})
	}
	results := race(t, root, perChild)
	if n := len(withCode(results, 0)); n != 6 {
		t.Errorf("shared readers must never block each other, %d succeeded: %s", n, detail(results))
	}
	if code := fridge(t, root, []string{"claim", "docs/**", "--task", "rewrite"}, runOpts{Actor: names[0]}).Code; code != 10 {
		t.Errorf("a writer must still wait, exited %d", code)
	}
}

func TestRegistryNeverEndsUpUnreadable(t *testing.T) {
	names := actorNames(10, "stress")
	root := bootstrap(t, "race-integrity", names...)
	perChild := []map[string]string{}
	for i, a := range names {
		target := "src/ui/**"
		if i%2 == 1 {
			target = "src/api/**"
		}
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": a, "RACE_TARGET": target,
		})
	}
	race(t, root, perChild)
	if code := fridge(t, root, []string{"doctor", "--json"}, runOpts{Actor: names[0]}).Code; code != 0 {
		t.Errorf("doctor exited %d", code)
	}
	if code := fridge(t, root, []string{"board"}, runOpts{Actor: names[0]}).Code; code != 0 {
		t.Errorf("board exited %d", code)
	}
	claims := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: names[0]}).JSON.ArrAt("data.claims")
	if len(claims) != 2 {
		t.Fatalf("one card per contended scope, got %d", len(claims))
	}
	owners := map[string]bool{}
	for _, raw := range claims {
		c, _ := raw.(jsonx.Obj)
		owners[c.Str("actorName")] = true
	}
	if len(owners) != 2 {
		t.Errorf("%d distinct owners", len(owners))
	}
}

// The regression test for the incident that started this project: two writers
// sharing one Markdown file destroyed each other's lines.
func TestTheOldWayReallyDoesLoseLines(t *testing.T) {
	names := actorNames(8, "writer")
	root := bootstrap(t, "lossy", names...)
	shared := filepath.Join(root, "shared-development-updates.md")
	writeFile(t, shared, "# Live updates\n")
	const lines = 25
	perChild := []map[string]string{}
	for _, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "write-shared", "FRIDGE_ACTOR": a,
			"SHARED_FILE": shared, "WRITE_COUNT": strconv.Itoa(lines),
		})
	}
	results := race(t, root, perChild)
	for _, r := range results {
		if r.Report == nil || int(r.Report.Num("written")) != lines {
			t.Fatalf("every process must believe it wrote every line: %s", detail(results))
		}
	}
	survived := 0
	for _, l := range strings.Split(readFile(t, shared), "\n") {
		if strings.HasPrefix(l, "- ") {
			survived++
		}
	}
	attempted := len(names) * lines
	if survived >= attempted {
		t.Errorf("expected lost lines with the naive approach, but all %d survived", attempted)
	}
	t.Logf("shared-file approach: %d lines written, %d survived, %d lost", attempted, survived, attempted-survived)
}

func TestNobodyErasesTheDoor(t *testing.T) {
	names := actorNames(8, "writer")
	root := bootstrap(t, "lossless", names...)
	const lines = 25
	perChild := []map[string]string{}
	for _, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "write-fridge", "FRIDGE_ACTOR": a, "WRITE_COUNT": strconv.Itoa(lines),
		})
	}
	results := race(t, root, perChild)
	for _, r := range results {
		if r.Report == nil || int(r.Report.Num("written")) != lines {
			t.Fatalf("every process must report success: %s", detail(results))
		}
	}
	pinned := []jsonx.Obj{}
	ids := map[string]bool{}
	for _, n := range notes(t, root) {
		if n.Str("type") == "note.note" {
			pinned = append(pinned, n)
			ids[n.Str("id")] = true
		}
	}
	want := len(names) * lines
	if len(pinned) != want {
		t.Errorf("expected %d notes, found %d", want, len(pinned))
	}
	if len(ids) != len(pinned) {
		t.Errorf("duplicate note ids: %d unique of %d", len(ids), len(pinned))
	}
	for _, a := range names {
		for i := 0; i < lines; i++ {
			want := fmt.Sprintf("%s line %d", a, i)
			count := 0
			for _, n := range pinned {
				if n.Str("actorName") == a && n.Str("summary") == want {
					count++
				}
			}
			if count != 1 {
				t.Errorf("%q appears %d times", want, count)
			}
		}
	}
	t.Logf("fridge approach:      %d notes written, %d survived, 0 lost", want, len(pinned))
}

func TestEveryNoteFileHasExactlyOneWriter(t *testing.T) {
	names := actorNames(4, "writer")
	root := bootstrap(t, "write-once", names...)
	perChild := []map[string]string{}
	for _, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "write-fridge", "FRIDGE_ACTOR": a, "WRITE_COUNT": "10",
		})
	}
	race(t, root, perChild)
	files := noteFiles(t, root)
	if len(files) == 0 {
		t.Fatal("no notes were written")
	}
	for _, f := range files {
		rec := readJSONFile(t, f)
		if !strings.Contains(filepath.Base(f), rec.Str("id")) {
			t.Errorf("%s does not embed its own id, so two writers could pick the same name", filepath.Base(f))
		}
		if rec.Str("actorName") == "" {
			t.Errorf("%s does not name its author", filepath.Base(f))
		}
	}
	before := modTimes(t, files)
	fridge(t, root, []string{"pin", "one more"}, runOpts{Actor: names[0]})
	after := modTimes(t, files)
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("writing a new note touched %s", filepath.Base(files[i]))
		}
	}
}

func modTimes(t *testing.T, files []string) []int64 {
	t.Helper()
	out := []int64{}
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, info.ModTime().UnixNano())
	}
	return out
}

func TestTheGeneratedDoorSurvivesConcurrentWriters(t *testing.T) {
	names := actorNames(8, "writer")
	root := bootstrap(t, "door-race", names...)
	perChild := []map[string]string{}
	for _, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "write-fridge", "FRIDGE_ACTOR": a, "WRITE_COUNT": "10",
		})
	}
	race(t, root, perChild)
	door := readDoor(t, root)
	if !strings.HasPrefix(door, "<!-- GENERATED by agent-fridge") {
		t.Errorf("the door is a torn half-write:\n%.200s", door)
	}
	if !strings.Contains(door, "DO NOT EDIT") {
		t.Errorf("the door lost its warning")
	}
	if code := fridge(t, root, []string{"render"}, runOpts{Actor: names[0]}).Code; code != 0 {
		t.Errorf("render exited %d", code)
	}
	if code := fridge(t, root, []string{"board", "--check"}, runOpts{Actor: names[0]}).Code; code != 0 {
		t.Errorf("board --check exited %d", code)
	}
}

func TestALiveCardIsNeverSwept(t *testing.T) {
	names := actorNames(6, "watcher")
	root := bootstrap(t, "stale-live", append([]string{"holder"}, names...)...)
	id := fridge(t, root, []string{"claim", "src/**", "--task", "long job", "--ttl", "2h", "--json"},
		runOpts{Actor: "holder"}).JSON.Str("data.claimId")
	perChild := []map[string]string{}
	for _, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": a, "RACE_TARGET": "src/api/routes.ts",
		})
	}
	results := race(t, root, perChild)
	if n := len(withCode(results, 10)); n != 6 {
		t.Errorf("everyone must be refused while the card is live, %d were: %s", n, detail(results))
	}
	claims := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "holder"}).JSON.ArrAt("data.claims")
	first, _ := claims[0].(jsonx.Obj)
	if first.Str("id") != id {
		t.Errorf("the card changed hands")
	}
}

func TestExactlyOneOfSixTakesOverAnExpiredLease(t *testing.T) {
	names := actorNames(6, "taker")
	root := bootstrap(t, "stale-takeover", append([]string{"holder"}, names...)...)
	fridge(t, root, []string{"claim", "src/api/**", "--task", "went to lunch", "--ttl", "1s"}, runOpts{Actor: "holder"})
	time.Sleep(1500 * time.Millisecond)
	perChild := []map[string]string{}
	for _, a := range names {
		perChild = append(perChild, map[string]string{
			"FRIDGE_RACE_MODE": "claim", "FRIDGE_ACTOR": a, "RACE_TARGET": "src/api/**",
		})
	}
	results := race(t, root, perChild)
	winners := withCode(results, 0)
	if len(winners) != 1 {
		t.Fatalf("exactly one takeover expected, got %d: %s", len(winners), detail(results))
	}
	if n := len(withCode(results, 10)); n != 5 {
		t.Errorf("expected five refusals, got %d: %s", n, detail(results))
	}
	expired := []jsonx.Obj{}
	for _, n := range notes(t, root) {
		if n.Str("type") == "claim.expired" {
			expired = append(expired, n)
		}
	}
	if len(expired) != 1 {
		t.Fatalf("the expiry must be recorded exactly once, found %d", len(expired))
	}
	if expired[0].Str("data.owner") != "holder" {
		t.Errorf("expiry blames %q", expired[0].Str("data.owner"))
	}
	live := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "holder"}).JSON.ArrAt("data.claims")
	if len(live) != 1 {
		t.Fatalf("the board shows %d cards", len(live))
	}
	card, _ := live[0].(jsonx.Obj)
	if card.Str("actorName") != winners[0].Report.Str("actor") {
		t.Errorf("the board disagrees with the winner")
	}
}

func TestAnExpiredOwnerCannotPretendNothingHappened(t *testing.T) {
	root := bootstrap(t, "stale-owner", "holder", "other")
	id := fridge(t, root, []string{"claim", "src/**", "--task", "slow", "--ttl", "1s", "--json"},
		runOpts{Actor: "holder"}).JSON.Str("data.claimId")
	time.Sleep(1500 * time.Millisecond)
	if code := fridge(t, root, []string{"heartbeat", "--json"}, runOpts{Actor: "holder"}).Code; code != 13 {
		t.Errorf("heartbeat on an expired lease exited %d, want 13", code)
	}
	fridge(t, root, []string{"reap"}, runOpts{Actor: "other"})
	if code := fridge(t, root, []string{"release", id}, runOpts{Actor: "holder"}).Code; code != 11 {
		t.Errorf("releasing a swept card exited %d, want 11", code)
	}
	if code := fridge(t, root, []string{"claim", "src/**", "--task", "again"}, runOpts{Actor: "holder"}).Code; code != 0 {
		t.Errorf("re-claiming exited %d", code)
	}
}

func TestAnyCommandFromTheOwnerCountsAsAHeartbeat(t *testing.T) {
	root := bootstrap(t, "stale-piggyback", "holder", "other")
	fridge(t, root, []string{"claim", "src/**", "--task", "long", "--ttl", "2s"}, runOpts{Actor: "holder"})
	time.Sleep(1200 * time.Millisecond)
	fridge(t, root, []string{"whoami"}, runOpts{Actor: "holder"})
	time.Sleep(1200 * time.Millisecond)
	if code := fridge(t, root, []string{"claim", "src/**", "--task", "steal"}, runOpts{Actor: "other"}).Code; code != 10 {
		t.Errorf("piggyback renewal did not hold the card open, exited %d", code)
	}
}

// Terminals get closed. Laptops sleep. Agents are killed mid-thought. None of
// that may leave the fridge in a state a human has to repair by hand.
func TestASIGKILLedAgentLeavesReadableState(t *testing.T) {
	root := bootstrap(t, "crash-basic", "ghost", "survivor")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append([]string{}, os.Environ()...)
	env = append(env, "FRIDGE_RACE_MODE=crash", "FRIDGE_ACTOR=ghost", "NO_COLOR=1",
		"CRASH_TARGET=src/api/**", "CRASH_TTL=3s")
	_, _, code := runChild(self, []string{"-test.run=^TestRaceHelperProcess$"}, root, env)
	if code == 0 {
		t.Fatalf("the child was asked politely instead of being killed (exit %d)", code)
	}
	if c := fridge(t, root, []string{"board"}, runOpts{Actor: "survivor"}).Code; c != 0 {
		t.Errorf("the board no longer reads, exited %d", c)
	}
	if c := fridge(t, root, []string{"claim", "src/api/**", "--task", "take over"}, runOpts{Actor: "survivor"}).Code; c != 10 {
		t.Errorf("still held while the lease runs, exited %d", c)
	}
	time.Sleep(3400 * time.Millisecond)
	if c := fridge(t, root, []string{"claim", "src/api/**", "--task", "take over"}, runOpts{Actor: "survivor"}).Code; c != 0 {
		t.Errorf("released by time with nobody to ask, exited %d", c)
	}
	seen := false
	for _, n := range notes(t, root) {
		if n.Str("type") == "claim.expired" {
			seen = true
		}
	}
	if !seen {
		t.Errorf("the fridge must say what happened")
	}
}

func TestALockLeftByADeadProcessIsBroken(t *testing.T) {
	root := bootstrap(t, "crash-mutex", "ghost", "survivor")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append([]string{}, os.Environ()...)
	env = append(env, "FRIDGE_RACE_MODE=crash", "FRIDGE_ACTOR=ghost", "NO_COLOR=1",
		"CRASH_TARGET=docs/**", "CRASH_TTL=1s", "CRASH_MODE=mutex")
	runChild(self, []string{"-test.run=^TestRaceHelperProcess$"}, root, env)
	lock := filepath.Join(root, ".fridge", "locks", "registry.lock.d")
	if !exists(lock) {
		t.Fatalf("the crash did not leave the registry lock behind")
	}
	r := fridge(t, root, []string{"claim", "src/**", "--task", "work anyway"}, runOpts{Actor: "survivor"})
	if r.Code != 0 {
		t.Errorf("the stale lock was not broken, exited %d: %s", r.Code, r.Stderr)
	}
}

func TestATornTempFileNeverBecomesARecord(t *testing.T) {
	root := bootstrap(t, "crash-tmp", "alice")
	fridge(t, root, []string{"claim", "src/**", "--task", "x"}, runOpts{Actor: "alice"})
	tmp := filepath.Join(root, ".fridge", "tmp")
	junk := filepath.Join(tmp, "claim-half-written.json.tmp")
	writeFile(t, junk, "{\"schema\":\"wcp/0.1/claim\",\"id\":\"clm_TR")
	if n := len(fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "alice"}).JSON.ArrAt("data.claims")); n != 1 {
		t.Errorf("a half-written temp file became a claim, %d on the board", n)
	}
	if code := fridge(t, root, []string{"doctor", "--json"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("fresh debris must not be reported as corruption, exited %d", code)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(junk, old, old); err != nil {
		t.Fatal(err)
	}
	d := fridge(t, root, []string{"doctor", "--json"}, runOpts{Actor: "alice"})
	found := false
	for _, raw := range d.JSON.ArrAt("data.findings") {
		f, _ := raw.(jsonx.Obj)
		if f.Str("id") == "tmp-junk" {
			found = true
		}
	}
	if !found {
		t.Errorf("old debris must be reported: %v", d.JSON.Get("data.findings"))
	}
	fridge(t, root, []string{"doctor", "--fix"}, runOpts{Actor: "alice"})
	entries, _ := os.ReadDir(tmp)
	for _, e := range entries {
		if strings.Contains(e.Name(), "half-written") {
			t.Errorf("debris was not cleaned up")
		}
	}
}

func TestACorruptedRecordIsQuarantined(t *testing.T) {
	root := bootstrap(t, "crash-corrupt", "alice")
	id := fridge(t, root, []string{"claim", "src/**", "--task", "x", "--json"},
		runOpts{Actor: "alice"}).JSON.Str("data.claimId")
	file := filepath.Join(root, ".fridge", "claims", id+".json")
	writeFile(t, file, "{\"schema\":\"wcp/0.1/claim\", \"id\": trunc")
	if code := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("one bad record broke the board, exited %d", code)
	}
	if code := fridge(t, root, []string{"doctor", "--check"}, runOpts{Actor: "alice"}).Code; code != 30 {
		t.Errorf("doctor --check exited %d, want 30", code)
	}
	if code := fridge(t, root, []string{"doctor", "--fix"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("doctor --fix exited %d", code)
	}
	if exists(file) {
		t.Errorf("the corrupt record was left in place")
	}
	entries, err := os.ReadDir(filepath.Join(root, ".fridge", "quarantine"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("quarantine holds %d files, want 1", len(entries))
	}
	if code := fridge(t, root, []string{"doctor", "--check"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("not clean after --fix, exited %d", code)
	}
}

func TestLosingTheWholeLiveDirectoryIsSurvivable(t *testing.T) {
	root := bootstrap(t, "crash-nuke", "alice")
	fridge(t, root, []string{"claim", "src/**", "--task", "x"}, runOpts{Actor: "alice"})
	fridge(t, root, []string{"pin", "important finding about the parser"}, runOpts{Actor: "alice"})
	before := len(notes(t, root))
	os.RemoveAll(filepath.Join(root, ".fridge", "claims"))
	os.RemoveAll(filepath.Join(root, ".fridge", "leases"))
	if n := len(fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "alice"}).JSON.ArrAt("data.claims")); n != 0 {
		t.Errorf("%d claims survived a nuked live directory", n)
	}
	if after := len(notes(t, root)); after != before {
		t.Errorf("the notes wall was touched: %d -> %d", before, after)
	}
	if code := fridge(t, root, []string{"claim", "src/**", "--task", "fresh start"}, runOpts{Actor: "alice"}).Code; code != 0 {
		t.Errorf("could not start fresh, exited %d", code)
	}
	if !strings.Contains(fridge(t, root, []string{"log"}, runOpts{Actor: "alice"}).Stdout, "important finding about the parser") {
		t.Errorf("history was lost")
	}
}
