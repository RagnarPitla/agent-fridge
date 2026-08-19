// SPDX-License-Identifier: Apache-2.0
// Conformance vectors, checked against this implementation. The vector files
// are language-neutral on purpose: this is the Go implementation of wcp/0.1
// loading exactly the same JSON the Node implementation loads, and it must
// produce exactly the same answers.
package paths

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/RagnarPitla/agent-fridge/internal/errs"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the test directory")
		}
		dir = parent
	}
}

// makeRepo mirrors test/helpers.mjs makeRepo. Fixtures live under .scratch/ in
// the repo, never under the system temp directory.
func makeRepo(t *testing.T, label string) string {
	t.Helper()
	root := filepath.Join(repoRoot(t), ".scratch", "gotest", label+"-"+strings.ReplaceAll(t.Name(), "/", "_"))
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"src/api/routes.ts": "export const routes = [];\n",
		"src/api/db.ts":     "export const db = {};\n",
		"src/ui/app.tsx":    "export const App = () => null;\n",
		"docs/guide.md":     "# guide\n",
		"README.md":         "# fixture\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return root
}

type vectorDoc struct {
	Protocol string            `json:"protocol"`
	Title    string            `json:"title"`
	Cases    []json.RawMessage `json:"cases"`
}

func loadVectors(t *testing.T, name string) vectorDoc {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "vectors", name))
	if err != nil {
		t.Fatal(err)
	}
	var doc vectorDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) == 0 {
		t.Fatalf("%s: no cases", name)
	}
	return doc
}

func isCode(err error, code string) bool {
	if app := errs.As(err); app != nil {
		return app.Code == code
	}
	return false
}

func TestVectorsPathNormalization(t *testing.T) {
	root := makeRepo(t, "vectors")
	doc := loadVectors(t, "path-normalization.json")
	for _, raw := range doc.Cases {
		var c struct {
			Name   string `json:"name"`
			Input  string `json:"input"`
			Cwd    string `json:"cwd"`
			Expect string `json:"expect"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		input := strings.ReplaceAll(c.Input, "<ROOT>", root)
		cwdRel := c.Cwd
		if cwdRel == "" {
			cwdRel = "."
		}
		cwd := filepath.Join(root, filepath.FromSlash(cwdRel))
		got, err := NormalizePattern(input, root, cwd)
		if c.Expect == "E_PATH_INVALID" {
			if !isCode(err, "E_PATH_INVALID") {
				t.Errorf("%s: %q must be rejected, got %q err=%v", c.Name, c.Input, got.Pattern, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.Name, err)
			continue
		}
		if got.Pattern != c.Expect {
			t.Errorf("%s: got %q want %q", c.Name, got.Pattern, c.Expect)
		}
	}
}

func TestVectorsScopeOverlap(t *testing.T) {
	doc := loadVectors(t, "scope-overlap.json")
	for _, raw := range doc.Cases {
		var c struct {
			Name    string   `json:"name"`
			A       []string `json:"a"`
			B       []string `json:"b"`
			Overlap bool     `json:"overlap"`
			Reason  string   `json:"reason"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		got, err := ScopesOverlap(Scope{Include: c.A, Exclude: []string{}}, Scope{Include: c.B, Exclude: []string{}}, false)
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.Name, err)
			continue
		}
		if got.Overlap != c.Overlap {
			t.Errorf("%s: %v vs %v -> overlap %v want %v (reason %s)", c.Name, c.A, c.B, got.Overlap, c.Overlap, got.Reason)
		}
		if c.Reason != "" && got.Reason != c.Reason {
			t.Errorf("%s: reason %q want %q", c.Name, got.Reason, c.Reason)
		}
	}
}

func TestVectorsGlobMatching(t *testing.T) {
	doc := loadVectors(t, "glob-matching.json")
	hits := func(t *testing.T, pattern, file string) bool {
		branches, err := ExpandBraces(pattern)
		if err != nil {
			t.Fatalf("expandBraces(%q): %v", pattern, err)
		}
		for _, p := range branches {
			re, err := PatternToRegExp(p, false)
			if err != nil {
				t.Fatalf("patternToRegExp(%q): %v", p, err)
			}
			if re.MatchString(file) {
				return true
			}
		}
		return false
	}
	for _, raw := range doc.Cases {
		var c struct {
			Name     string   `json:"name"`
			Pattern  string   `json:"pattern"`
			Matches  []string `json:"matches"`
			Rejects  []string `json:"rejects"`
			Insens   bool     `json:"caseInsensitive"`
			Reserved string   `json:"-"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		for _, m := range c.Matches {
			if !hits(t, c.Pattern, m) {
				t.Errorf("%s: %s should match %s", c.Name, c.Pattern, m)
			}
		}
		for _, m := range c.Rejects {
			if hits(t, c.Pattern, m) {
				t.Errorf("%s: %s should not match %s", c.Name, c.Pattern, m)
			}
		}
	}
}

func TestVectorsBraceExpansion(t *testing.T) {
	doc := loadVectors(t, "brace-expansion.json")
	for _, raw := range doc.Cases {
		var c struct {
			Name        string   `json:"name"`
			Input       string   `json:"input"`
			Expect      []string `json:"expect"`
			ExpectError string   `json:"expect_error"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		got, err := ExpandBraces(c.Input)
		if c.ExpectError != "" {
			if !isCode(err, c.ExpectError) {
				t.Errorf("%s: %q must fail with %s, got %v err=%v", c.Name, c.Input, c.ExpectError, got, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", c.Name, err)
			continue
		}
		a := append([]string{}, got...)
		b := append([]string{}, c.Expect...)
		sort.Strings(a)
		sort.Strings(b)
		if strings.Join(a, "\x00") != strings.Join(b, "\x00") {
			t.Errorf("%s: got %v want %v", c.Name, a, b)
		}
	}
}

func TestEveryVectorFileDeclaresItsProtocol(t *testing.T) {
	dir := filepath.Join(repoRoot(t), "vectors")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		count++
		doc := loadVectors(t, e.Name())
		if doc.Protocol != "wcp/0.1" {
			t.Errorf("%s: must declare the protocol it targets, got %q", e.Name(), doc.Protocol)
		}
		for i, raw := range doc.Cases {
			var c struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &c); err != nil || c.Name == "" {
				t.Errorf("%s: case %d needs a name", e.Name(), i)
			}
		}
	}
	if count < 4 {
		t.Fatalf("the spec promises conformance vectors, found %d files", count)
	}
}
