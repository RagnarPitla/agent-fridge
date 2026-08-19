// SPDX-License-Identifier: Apache-2.0
// Unit tests for the small deterministic helpers. Where the Node CLI is
// available these check the two implementations agree value for value, because
// every one of these functions ends up in a filename, an id, or a hash that
// both implementations have to read back.
package util

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/errs"
)

// nodeEval runs one expression against the Node reference helpers and returns
// its JSON result. It skips the test when node is missing.
func nodeEval(t *testing.T, expr string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH")
	}
	src := "import * as u from './src/core/util.mjs';\n" +
		"process.stdout.write(JSON.stringify(" + expr + "));\n"
	cmd := exec.Command(node, "--input-type=module", "-e", src)
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	return string(out)
}

func TestSlugMatchesNode(t *testing.T) {
	names := []string{"alice", "Alice", "Claude Code", "  spaced  out  ",
		"UPPER_snake.case", "a/b/c", "---", "", "..", "9lives",
		"a-very-long-agent-name-that-will-be-truncated-somewhere"}
	got := make([]string, len(names))
	for i, n := range names {
		got[i] = Slug(n)
	}
	blob, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	arg, _ := json.Marshal(names)
	want := nodeEval(t, "("+string(arg)+").map((n) => u.slug(n))")
	if string(blob) != want {
		t.Errorf("slug disagrees with node:\ngo:   %s\nnode: %s", blob, want)
	}
	// Independent of node, the invariants the store depends on.
	for _, n := range names {
		s := Slug(n)
		if s == "" {
			t.Errorf("slug(%q) is empty; every actor needs a filename", n)
		}
		if len(s) > 24 {
			t.Errorf("slug(%q) = %q is longer than 24", n, s)
		}
		if strings.HasPrefix(s, "-") || strings.HasSuffix(s, "-") {
			t.Errorf("slug(%q) = %q has a dangling separator", n, s)
		}
	}
	if Slug("???") != "anon" {
		t.Errorf("an unnameable actor should be %q, got %q", "anon", Slug("???"))
	}
}

func TestHashesMatchNode(t *testing.T) {
	inputs := []string{"", "a", "the quick brown fox", "{\"a\":1}\n"}
	type pair struct {
		Sha   string `json:"sha"`
		Short string `json:"short"`
	}
	got := make([]pair, len(inputs))
	for i, s := range inputs {
		got[i] = pair{SHA256(s), ShortHash(s)}
	}
	blob, _ := json.Marshal(got)
	arg, _ := json.Marshal(inputs)
	want := nodeEval(t, "("+string(arg)+").map((s) => ({ sha: u.sha256(s), short: u.shortHash(s) }))")
	if string(blob) != want {
		t.Errorf("hashes disagree with node:\ngo:   %s\nnode: %s", blob, want)
	}
	if !strings.HasPrefix(SHA256("x"), "sha256:") {
		t.Error("sha256 lost its prefix")
	}
	if len(ShortHash("x")) != 12 {
		t.Errorf("shortHash is %d characters, want 12", len(ShortHash("x")))
	}
	if len(HostID()) != 23 {
		t.Errorf("hostId is %d characters, want 23", len(HostID()))
	}
}

func TestCompactTsMatchesNode(t *testing.T) {
	stamps := []string{"2024-01-02T03:04:05.006Z", "1999-12-31T23:59:59.999Z", "2024-06-01T00:00:00.000Z"}
	got := make([]string, len(stamps))
	for i, s := range stamps {
		got[i] = CompactTs(s)
	}
	blob, _ := json.Marshal(got)
	arg, _ := json.Marshal(stamps)
	want := nodeEval(t, "("+string(arg)+").map((s) => u.compactTs(s))")
	if string(blob) != want {
		t.Errorf("compactTs disagrees with node:\ngo:   %s\nnode: %s", blob, want)
	}
	if got[0] != "20240102T030405006Z" {
		t.Errorf("compactTs = %q", got[0])
	}
}

func TestParseDuration(t *testing.T) {
	ok := map[string]int64{
		"500ms": 500, "0ms": 0, "30s": 30_000, "15m": 900_000,
		"2h": 7_200_000, "1d": 86_400_000, "  45s  ": 45_000,
	}
	for in, want := range ok {
		got, err := ParseDuration(in, "ttl")
		if err != nil {
			t.Errorf("parseDuration(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseDuration(%q) = %d, want %d", in, got, want)
		}
	}
	for _, in := range []string{"90", "s", "-5s", "1w", "1.5h", "30 s", "abc", ""} {
		if _, err := ParseDuration(in, "ttl"); err == nil {
			t.Errorf("parseDuration(%q) was accepted", in)
		} else if e := errs.As(err); e == nil || e.Code != "E_USAGE" {
			t.Errorf("parseDuration(%q) returned %v, want E_USAGE", in, err)
		}
	}
	// The hint tells a bare number apart from real nonsense.
	if _, err := ParseDuration("90", "ttl"); !strings.Contains(errs.As(err).Hint, "90s") {
		t.Errorf("a unitless duration should suggest 90s, hint was %q", errs.As(err).Hint)
	}
}

func TestHumanMsMatchesNode(t *testing.T) {
	values := []int64{-1, -60_000, 0, 999, 1_000, 59_999, 60_000, 61_000, 3_599_000, 3_600_000, 3_660_000, 86_400_000}
	got := make([]string, len(values))
	for i, v := range values {
		got[i] = HumanMs(v)
	}
	blob, _ := json.Marshal(got)
	arg, _ := json.Marshal(values)
	want := nodeEval(t, "("+string(arg)+").map((v) => u.humanMs(v))")
	if string(blob) != want {
		t.Errorf("humanMs disagrees with node:\ngo:   %s\nnode: %s", blob, want)
	}
}

func TestMulberry32MatchesNode(t *testing.T) {
	next := Mulberry32(42)
	got := make([]float64, 8)
	for i := range got {
		got[i] = next()
	}
	blob, _ := json.Marshal(got)
	want := nodeEval(t, "(() => { const r = u.mulberry32(42); return Array.from({ length: 8 }, () => r()); })()")
	if string(blob) != want {
		t.Errorf("the seeded generator diverges, so simulate would not replay:\ngo:   %s\nnode: %s", blob, want)
	}
	// Same seed, same stream. That is the whole point of a seeded run.
	a, b := Mulberry32(7), Mulberry32(7)
	for i := 0; i < 32; i++ {
		x, y := a(), b()
		if x != y {
			t.Fatalf("draw %d differs between two generators with the same seed", i)
		}
		if x < 0 || x >= 1 {
			t.Fatalf("draw %d is %v, outside [0, 1)", i, x)
		}
	}
}

func TestIDsAreSortableAndRecognisable(t *testing.T) {
	prev := ""
	for i := 0; i < 200; i++ {
		id := NewID("clm")
		if !IsID(id) {
			t.Fatalf("%q is not recognised as an id", id)
		}
		if len(id) != len("clm_")+26 {
			t.Fatalf("%q is the wrong length", id)
		}
		body := id[4:]
		if strings.ToUpper(body) != body {
			t.Fatalf("%q is not upper-case Crockford base32", id)
		}
		// Ids minted in order must sort in order, which is what makes the door
		// stable without a clock lookup.
		if prev != "" && body < prev {
			t.Fatalf("%q sorts before its predecessor %q", body, prev)
		}
		prev = body
	}
	for _, bad := range []string{"", "clm", "clm_", "clm_short", "CLM_" + strings.Repeat("A", 26), "clm_" + strings.Repeat("a", 26)} {
		if IsID(bad) {
			t.Errorf("%q was accepted as an id", bad)
		}
	}
}

func TestISORoundTrip(t *testing.T) {
	when := time.Date(2024, 3, 4, 5, 6, 7, 8_000_000, time.UTC)
	iso := NowISO(when)
	if iso != "2024-03-04T05:06:07.008Z" {
		t.Fatalf("nowIso = %q", iso)
	}
	back, ok := ParseISO(iso)
	if !ok || !back.Equal(when) {
		t.Fatalf("round trip gave %v (ok=%v)", back, ok)
	}
	ms, ok := ParseMs(iso)
	if !ok || ms != when.UnixMilli() {
		t.Fatalf("parseMs = %d (ok=%v), want %d", ms, ok, when.UnixMilli())
	}
	if _, ok := ParseISO("not a time"); ok {
		t.Error("nonsense parsed as a timestamp")
	}
	if got := nodeEval(t, "u.compactTs('"+iso+"')"); got != "\""+CompactTs(iso)+"\"" {
		t.Errorf("compactTs round trip disagrees with node: %s", got)
	}
}

func TestRandomTokenIsUnguessable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		tok := RandomToken()
		if len(tok) != 32 {
			t.Fatalf("token %q is %d characters, want 32", tok, len(tok))
		}
		if strings.ContainsAny(tok, "+/=") {
			t.Fatalf("token %q is not URL-safe", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q came up twice", tok)
		}
		seen[tok] = true
	}
}

func TestJitterStaysInBand(t *testing.T) {
	for i := 0; i < 500; i++ {
		got := Jitter(100, 0.3)
		if got < 70 || got > 130 {
			t.Fatalf("jitter(100, 0.3) = %d, outside the band", got)
		}
	}
	if Jitter(1, 0.9) < 1 {
		t.Error("jitter went to zero and would spin")
	}
}

func TestProcessAlive(t *testing.T) {
	if !ProcessAlive(os.Getpid()) {
		t.Error("this process reports itself as dead")
	}
	if ProcessAlive(0) {
		t.Error("pid 0 is not a process we can own")
	}
	if ProcessAlive(-1) {
		t.Error("a negative pid is not a process")
	}
}
