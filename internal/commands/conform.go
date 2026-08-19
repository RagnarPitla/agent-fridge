// SPDX-License-Identifier: Apache-2.0
// The conformance harness. Layer 3 of the package: the thing that makes the
// protocol document real rather than decorative. Any implementation of the
// protocol in any language runs the same vector files and must produce the
// same answers.
//
// The vectors are embedded in the binary so that a downloaded release can
// prove itself with no checkout, no network, and no second file to install.
package commands

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/output"
	"github.com/RagnarPitla/agent-fridge/internal/paths"
	"github.com/RagnarPitla/agent-fridge/vectors"
)

type conformFixture struct {
	Dirs  []string          `json:"dirs"`
	Files map[string]string `json:"files"`
}

type conformDoc struct {
	Protocol string            `json:"protocol"`
	Title    string            `json:"title"`
	Fixture  *conformFixture   `json:"fixture"`
	Cases    []json.RawMessage `json:"cases"`
}

type conformCase struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type conformSuite struct {
	Suite    string        `json:"suite"`
	Title    string        `json:"title"`
	Protocol string        `json:"protocol"`
	Status   string        `json:"status"`
	Reason   string        `json:"reason,omitempty"`
	Cases    []conformCase `json:"cases"`
	Passed   int           `json:"passed"`
	Failed   int           `json:"failed"`
}

// conformSource abstracts "the embedded vectors" and "a directory the user
// pointed at", so the runner below does not care which it got.
type conformSource struct {
	label    string
	bundled  bool
	names    []string
	readFile func(name string) ([]byte, error)
}

func conformVectors(dir string) (*conformSource, error) {
	if dir == "" {
		entries, err := fs.ReadDir(vectors.FS, vectors.Dir)
		if err != nil {
			return nil, errs.New("E_INTERNAL", "embedded vectors are unreadable: "+err.Error())
		}
		src := &conformSource{label: "embedded in this binary", bundled: true}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				src.names = append(src.names, e.Name())
			}
		}
		src.readFile = func(name string) ([]byte, error) { return vectors.FS.ReadFile(name) }
		sort.Strings(src.names)
		return src, nil
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, errs.New("E_USAGE", "cannot resolve --vectors "+dir+": "+err.Error())
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return nil, errs.New("E_NOT_FOUND", "no vector directory at "+abs).
			WithHint("Pass --vectors <dir>, or omit it to use the vectors embedded in this binary.")
	}
	src := &conformSource{label: abs, bundled: false}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			src.names = append(src.names, e.Name())
		}
	}
	src.readFile = func(name string) ([]byte, error) { return os.ReadFile(filepath.Join(abs, name)) }
	sort.Strings(src.names)
	return src, nil
}

// materializeConformFixture builds the directory tree a suite declares it
// needs. Doing this rather than assuming the caller's working directory is what
// makes a conformance run reproducible on a stranger's machine.
func materializeConformFixture(fixture *conformFixture, dest string) (string, error) {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", err
	}
	if fixture != nil {
		for _, d := range fixture.Dirs {
			if err := os.MkdirAll(filepath.Join(dest, filepath.FromSlash(d)), 0o755); err != nil {
				return "", err
			}
		}
		for rel, body := range fixture.Files {
			p := filepath.Join(dest, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
				return "", err
			}
		}
	}
	real, err := filepath.EvalSymlinks(dest)
	if err != nil {
		return dest, nil
	}
	return real, nil
}

// Each runner takes one raw case and returns "" when it conforms, or a string
// explaining the disagreement. Deliberately dumb: a conformance harness that
// shares clever helpers with the implementation is testing nothing.
var conformRunners = map[string]func(raw json.RawMessage, root string) string{
	"path-normalization": func(raw json.RawMessage, root string) string {
		var c struct {
			Name   string `json:"name"`
			Input  string `json:"input"`
			Cwd    string `json:"cwd"`
			Expect string `json:"expect"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return "unreadable case: " + err.Error()
		}
		input := strings.ReplaceAll(c.Input, "<ROOT>", root)
		cwd := root
		if c.Cwd != "" {
			cwd = filepath.Join(root, filepath.FromSlash(c.Cwd))
		}
		got, err := paths.NormalizePattern(input, root, cwd)
		if c.Expect == "E_PATH_INVALID" {
			if err == nil {
				return fmt.Sprintf("expected rejection E_PATH_INVALID, got %q", got.Pattern)
			}
			if app := errs.As(err); app != nil && app.Code == "E_PATH_INVALID" {
				return ""
			}
			return "expected E_PATH_INVALID, got " + err.Error()
		}
		if err != nil {
			return fmt.Sprintf("expected %q, got error %s", c.Expect, err.Error())
		}
		if got.Pattern != c.Expect {
			return fmt.Sprintf("expected %q, got %q", c.Expect, got.Pattern)
		}
		return ""
	},

	"scope-overlap": func(raw json.RawMessage, _ string) string {
		var c struct {
			Name    string   `json:"name"`
			A       []string `json:"a"`
			B       []string `json:"b"`
			Overlap bool     `json:"overlap"`
			Reason  string   `json:"reason"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return "unreadable case: " + err.Error()
		}
		got, err := paths.ScopesOverlap(paths.Scope{Include: c.A}, paths.Scope{Include: c.B}, false)
		if err != nil {
			return "overlap check errored: " + err.Error()
		}
		if got.Overlap != c.Overlap {
			return fmt.Sprintf("expected overlap=%v, got %v", c.Overlap, got.Overlap)
		}
		if c.Reason != "" && got.Reason != c.Reason {
			return fmt.Sprintf("expected reason %s, got %s", c.Reason, got.Reason)
		}
		return ""
	},

	"glob-matching": func(raw json.RawMessage, _ string) string {
		var c struct {
			Name    string   `json:"name"`
			Pattern string   `json:"pattern"`
			Matches []string `json:"matches"`
			Rejects []string `json:"rejects"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return "unreadable case: " + err.Error()
		}
		hits := func(file string) (bool, error) {
			expanded, err := paths.ExpandBraces(c.Pattern)
			if err != nil {
				return false, err
			}
			return paths.MatchesAny(expanded, file, false)
		}
		for _, m := range c.Matches {
			ok, err := hits(m)
			if err != nil {
				return "match errored: " + err.Error()
			}
			if !ok {
				return fmt.Sprintf("%s should match %s", c.Pattern, m)
			}
		}
		for _, m := range c.Rejects {
			ok, err := hits(m)
			if err != nil {
				return "match errored: " + err.Error()
			}
			if ok {
				return fmt.Sprintf("%s should not match %s", c.Pattern, m)
			}
		}
		return ""
	},

	"brace-expansion": func(raw json.RawMessage, _ string) string {
		var c struct {
			Name        string   `json:"name"`
			Input       string   `json:"input"`
			Expect      []string `json:"expect"`
			ExpectError string   `json:"expect_error"`
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return "unreadable case: " + err.Error()
		}
		got, err := paths.ExpandBraces(c.Input)
		if c.ExpectError != "" {
			if err == nil {
				return "expected " + c.ExpectError + ", got no error"
			}
			if app := errs.As(err); app != nil && app.Code == c.ExpectError {
				return ""
			}
			return "expected " + c.ExpectError + ", got " + err.Error()
		}
		if err != nil {
			return "unexpected error: " + err.Error()
		}
		a := append([]string(nil), got...)
		b := append([]string(nil), c.Expect...)
		sort.Strings(a)
		sort.Strings(b)
		if strings.Join(a, "\x00") != strings.Join(b, "\x00") {
			return fmt.Sprintf("expected %v, got %v", b, a)
		}
		return ""
	},
}

func cmdConform(ctx *Ctx) (int, error) {
	src, err := conformVectors(ctx.Flags.Str("vectors"))
	if err != nil {
		return 0, err
	}
	if len(src.names) == 0 {
		return 0, errs.New("E_NOT_FOUND", "no vector files in "+src.label).WithHint("Vector files are named <suite>.json.")
	}

	var only map[string]bool
	if s := ctx.Flags.Str("suite"); s != "" {
		only = map[string]bool{}
		for _, part := range strings.Split(s, ",") {
			only[strings.TrimSpace(part)] = true
		}
	}

	scratchParent := ctx.Cwd
	if scratchParent == "" {
		scratchParent, _ = os.Getwd()
	}
	scratch, err := os.MkdirTemp(scratchParent, ".fridge-conform-")
	if err != nil {
		return 0, errs.New("E_PERMISSION", "cannot create a scratch directory in "+scratchParent+": "+err.Error()).WithHint("Run conform from a writable directory.")
	}
	defer os.RemoveAll(scratch)

	var suites []conformSuite
	total, failed, skipped := 0, 0, 0

	for _, name := range src.names {
		suite := strings.TrimSuffix(name, ".json")
		if only != nil && !only[suite] {
			continue
		}

		data, readErr := src.readFile(name)
		if readErr != nil {
			return 0, errs.New("E_STATE_CORRUPT", "vector file "+name+" is unreadable: "+readErr.Error())
		}
		var doc conformDoc
		if err := json.Unmarshal(data, &doc); err != nil {
			return 0, errs.New("E_STATE_CORRUPT", "vector file "+name+" is not valid JSON: "+err.Error())
		}

		title := doc.Title
		if title == "" {
			title = suite
		}

		if doc.Protocol != "" && doc.Protocol != brand.Protocol {
			suites = append(suites, conformSuite{Suite: suite, Title: title, Protocol: doc.Protocol, Status: "skipped",
				Reason: "vectors target " + doc.Protocol + ", this build speaks " + brand.Protocol, Cases: []conformCase{}})
			skipped += len(doc.Cases)
			continue
		}

		run, ok := conformRunners[suite]
		if !ok {
			suites = append(suites, conformSuite{Suite: suite, Title: title, Protocol: doc.Protocol, Status: "skipped",
				Reason: "no runner for this suite in this implementation", Cases: []conformCase{}})
			skipped += len(doc.Cases)
			continue
		}

		root, ferr := materializeConformFixture(doc.Fixture, filepath.Join(scratch, suite))
		if ferr != nil {
			return 0, errs.New("E_PERMISSION", "cannot materialize the fixture for "+suite+": "+ferr.Error())
		}

		cases := []conformCase{}
		pass, fail := 0, 0
		for _, raw := range doc.Cases {
			total++
			var named struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(raw, &named)
			detail := run(raw, root)
			if detail != "" {
				fail++
				failed++
				cases = append(cases, conformCase{Name: named.Name, OK: false, Detail: detail})
			} else {
				pass++
				cases = append(cases, conformCase{Name: named.Name, OK: true})
			}
		}
		status := "pass"
		if fail > 0 {
			status = "fail"
		}
		suites = append(suites, conformSuite{Suite: suite, Title: title, Protocol: doc.Protocol, Status: status,
			Cases: cases, Passed: pass, Failed: fail})
	}

	impl := brand.Product + " (go) " + brand.Version

	// The output encoder is a closed type switch over jsonx.Obj and jsonx.Arr,
	// so the report is converted rather than handed over as typed structs.
	suiteJSON := func(s conformSuite, failuresOnly bool) jsonx.Obj {
		cases := jsonx.Arr{}
		for _, c := range s.Cases {
			if failuresOnly && c.OK {
				continue
			}
			entry := jsonx.Obj{"name": c.Name, "ok": c.OK, "detail": nil}
			if c.Detail != "" {
				entry["detail"] = c.Detail
			}
			cases = append(cases, entry)
		}
		var reason any
		if s.Reason != "" {
			reason = s.Reason
		}
		var proto any
		if s.Protocol != "" {
			proto = s.Protocol
		}
		return jsonx.Obj{
			"suite": s.Suite, "title": s.Title, "protocol": proto, "status": s.Status,
			"reason": reason, "cases": cases, "passed": s.Passed, "failed": s.Failed,
		}
	}
	reported := jsonx.Arr{}
	for _, s := range suites {
		reported = append(reported, suiteJSON(s, !ctx.Verbose))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Conformance: %s against %s\n", impl, brand.Protocol)
	suffix := ""
	if src.bundled {
		suffix = " (bundled)"
	}
	fmt.Fprintf(&b, "vectors: %s%s\n\n", src.label, suffix)
	b.WriteString("| suite | cases | result |\n|---|---:|---|\n")
	for _, s := range suites {
		label := "PASS"
		switch s.Status {
		case "skipped":
			label = "SKIPPED (" + s.Reason + ")"
		case "fail":
			label = fmt.Sprintf("FAIL (%d)", s.Failed)
		}
		fmt.Fprintf(&b, "| %s | %d | %s |\n", s.Suite, s.Passed+s.Failed, label)
	}
	for _, s := range suites {
		for _, c := range s.Cases {
			if !c.OK {
				fmt.Fprintf(&b, "\n  %s / %s\n    %s\n", s.Suite, c.Name, c.Detail)
			}
		}
	}
	b.WriteString("\n")
	if failed == 0 {
		fmt.Fprintf(&b, "Result: CONFORMANT. %d case(s) passed", total)
		if skipped > 0 {
			fmt.Fprintf(&b, ", %d skipped", skipped)
		}
		b.WriteString(".")
	} else {
		fmt.Fprintf(&b, "Result: NOT CONFORMANT. %d of %d case(s) disagree with %s.", failed, total, brand.Protocol)
	}

	ctx.emit("conform", output.Result{
		Data: jsonx.Obj{
			"implementation": impl,
			"protocol":       brand.Protocol,
			"vectorDir":      src.label,
			"bundled":        src.bundled,
			"totals": jsonx.Obj{
				"cases": total, "passed": total - failed, "failed": failed, "skipped": skipped,
			},
			"conformant": failed == 0,
			"suites":     reported,
		},
		Text: b.String(),
	})

	if failed > 0 {
		return 0, errs.New("E_NONCONFORMANT", fmt.Sprintf("%d conformance case(s) failed.", failed)).
			WithHint("Run with --verbose to see every case, or --suite <name> to narrow.")
	}
	return 0, nil
}
