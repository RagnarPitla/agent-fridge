// SPDX-License-Identifier: Apache-2.0
// Path normalization, a dependency-free glob subset, and conservative overlap.
// Invariant I3: Overlap may say yes when a real conflict would not have
// happened. It must never say no when the materialized sets intersect or the
// prefixes nest. Ported from src/core/paths.mjs and checked against the
// language-neutral vectors in vectors/.
package paths

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/RagnarPitla/agent-fridge/internal/errs"
)

var (
	metaRe        = regexp.MustCompile(`[*?\[{]`)
	winReservedRe = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\.|$)`)
	extGlobRe     = regexp.MustCompile(`[!@+?*]\(`)
	digitsOnlyRe  = regexp.MustCompile(`^\d+$`)
	multiSlashRe  = regexp.MustCompile(`/{2,}`)
	jsEscapeRe    = regexp.MustCompile(`[.*+?^${}()|\[\]\\]`)
	classEscapeRe = regexp.MustCompile(`[\\^\]\[]`)
	newlineRe     = regexp.MustCompile(`[\n\r]`)
)

var reservedRoots = []string{".fridge", ".git"}

const isWindows = runtime.GOOS == "windows"

// CaseFold lowercases only when the workspace is configured case-insensitive.
func CaseFold(s string, insensitive bool) string {
	if insensitive {
		return strings.ToLower(s)
	}
	return s
}

// DefaultCaseInsensitive resolves config.paths.caseSensitivity, where "auto"
// probes the platform: Windows and macOS are insensitive by default.
func DefaultCaseInsensitive(setting string) bool {
	switch setting {
	case "sensitive":
		return false
	case "insensitive":
		return true
	default:
		return runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	}
}

// LiteralPrefix is the part of a pattern before its first metacharacter, cut
// back to the last slash. It is the whole basis of the nesting test.
func LiteralPrefix(pattern string) string {
	loc := metaRe.FindStringIndex(pattern)
	if loc == nil {
		return strings.TrimRight(pattern, "/")
	}
	head := pattern[:loc[0]]
	cut := strings.LastIndex(head, "/")
	if cut == -1 {
		return ""
	}
	return head[:cut]
}

// IsPrefixPath reports whether a is b, or a directory containing b.
func IsPrefixPath(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	if a == b {
		return true
	}
	return strings.HasPrefix(b, a+"/")
}

// ExpandBraces turns {a,b} alternation into concrete patterns before matching.
// Nesting is allowed; an unterminated brace is refused rather than guessed.
func ExpandBraces(pattern string) ([]string, error) {
	open := strings.IndexByte(pattern, '{')
	if open == -1 {
		return []string{pattern}, nil
	}
	depth := 0
	close := -1
	for i := open; i < len(pattern); i++ {
		if pattern[i] == '{' {
			depth++
		} else if pattern[i] == '}' {
			depth--
			if depth == 0 {
				close = i
				break
			}
		}
	}
	if close == -1 {
		return nil, errs.New("E_PATH_INVALID", "Unterminated brace in pattern: "+pattern)
	}
	head := pattern[:open]
	tail := pattern[close+1:]
	body := pattern[open+1 : close]
	parts := []string{}
	depth2 := 0
	cur := strings.Builder{}
	for _, ch := range body {
		if ch == '{' {
			depth2++
		}
		if ch == '}' {
			depth2--
		}
		if ch == ',' && depth2 == 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		} else {
			cur.WriteRune(ch)
		}
	}
	parts = append(parts, cur.String())
	out := []string{}
	for _, p := range parts {
		sub, err := ExpandBraces(head + p + tail)
		if err != nil {
			return nil, err
		}
		out = append(out, sub...)
	}
	return out, nil
}

func segmentToRegex(seg, pattern string) (string, error) {
	var re strings.Builder
	for i := 0; i < len(seg); i++ {
		c := seg[i]
		switch c {
		case '*':
			re.WriteString("[^/]*")
		case '?':
			re.WriteString("[^/]")
		case '[':
			j := i + 1
			neg := false
			if j < len(seg) && (seg[j] == '!' || seg[j] == '^') {
				neg = true
				j++
			}
			var cls strings.Builder
			for j < len(seg) && seg[j] != ']' {
				cls.WriteString(classEscapeRe.ReplaceAllString(string(seg[j]), `\$0`))
				j++
			}
			if j >= len(seg) {
				return "", errs.New("E_PATH_INVALID", "Unterminated character class in: "+pattern)
			}
			if cls.Len() == 0 {
				return "", errs.New("E_PATH_INVALID", "Empty character class in: "+pattern)
			}
			re.WriteByte('[')
			if neg {
				re.WriteByte('^')
			}
			re.WriteString(cls.String())
			re.WriteByte(']')
			i = j
		default:
			re.WriteString(jsEscapeRe.ReplaceAllString(string(c), `\$0`))
		}
	}
	return re.String(), nil
}

// PatternToRegExp compiles one brace-free pattern into an anchored matcher.
// Extended globs and negation are refused: rejecting is better than guessing.
func PatternToRegExp(pattern string, insensitive bool) (*regexp.Regexp, error) {
	if extGlobRe.MatchString(pattern) {
		return nil, errs.New("E_PATH_INVALID", "Extended globs are not supported: "+pattern).
			WithHint("Supported: * ** ? [abc] {a,b}. Use --exclude instead of negation.")
	}
	if strings.HasPrefix(pattern, "!") {
		return nil, errs.New("E_PATH_INVALID", "Negated patterns are not supported: "+pattern).WithHint("Use --exclude.")
	}
	segs := strings.Split(pattern, "/")
	var re strings.Builder
	re.WriteString("^")
	for idx, seg := range segs {
		last := idx == len(segs)-1
		if seg == "**" {
			if last {
				re.WriteString(".*")
			} else {
				re.WriteString("(?:.*/)?")
			}
			continue
		}
		if strings.Contains(seg, "**") {
			return nil, errs.New("E_PATH_INVALID", "'**' must be a whole path segment: "+pattern)
		}
		part, err := segmentToRegex(seg, pattern)
		if err != nil {
			return nil, err
		}
		re.WriteString(part)
		if !last {
			re.WriteString("/")
		}
	}
	re.WriteString("$")
	src := re.String()
	if insensitive {
		src = "(?i)" + src
	}
	compiled, err := regexp.Compile(src)
	if err != nil {
		return nil, errs.New("E_PATH_INVALID", "Unsupported pattern: "+pattern)
	}
	return compiled, nil
}

// Matcher is a compiled set of patterns. Compiling once and matching many
// times keeps materialization linear, and it surfaces an unsupported pattern as
// E_PATH_INVALID at compile time rather than silently never matching.
type Matcher struct {
	res []*regexp.Regexp
}

// CompileMatchers expands braces and compiles every resulting pattern.
func CompileMatchers(patterns []string, insensitive bool) (*Matcher, error) {
	m := &Matcher{}
	for _, p := range patterns {
		expanded, err := ExpandBraces(p)
		if err != nil {
			return nil, err
		}
		for _, e := range expanded {
			re, err := PatternToRegExp(e, insensitive)
			if err != nil {
				return nil, err
			}
			m.res = append(m.res, re)
		}
	}
	return m, nil
}

// Match reports whether any compiled pattern matches the file.
func (m *Matcher) Match(file string) bool {
	for _, re := range m.res {
		if re.MatchString(file) {
			return true
		}
	}
	return false
}

// MatchesAny reports whether any pattern, after brace expansion, matches file.
func MatchesAny(patterns []string, file string, insensitive bool) (bool, error) {
	m, err := CompileMatchers(patterns, insensitive)
	if err != nil {
		return false, err
	}
	return m.Match(file), nil
}

// Normalized is the result of turning one user-supplied path into a
// repo-relative POSIX pattern.
type Normalized struct {
	Pattern   string
	DirIntent bool
	IsGlob    bool
}

// NormalizePattern applies section 6.1 of the protocol in order: separator
// folding, resolution against the cwd, relativisation to the root, rejection of
// escapes and reserved locations, then symlink containment.
func NormalizePattern(input, root, cwd string) (Normalized, error) {
	var zero Normalized
	if strings.TrimSpace(input) == "" {
		return zero, errs.New("E_PATH_INVALID", "Empty path.")
	}
	raw := input
	if strings.ContainsRune(raw, 0) {
		return zero, errs.New("E_PATH_INVALID", "Path contains a NUL byte.")
	}
	if newlineRe.MatchString(raw) {
		return zero, errs.New("E_PATH_INVALID", "Path contains a newline.")
	}
	if len(raw) > 4096 {
		return zero, errs.New("E_PATH_INVALID", "Path is longer than 4096 characters.")
	}
	if isWindows {
		raw = strings.ReplaceAll(raw, `\`, "/")
	}
	if strings.HasPrefix(raw, "~") {
		return zero, errs.New("E_PATH_INVALID", "Home-relative paths are not accepted.")
	}
	if strings.HasPrefix(raw, "//") || strings.HasPrefix(raw, `\\`) {
		return zero, errs.New("E_PATH_INVALID", "UNC paths are not accepted.")
	}
	raw = multiSlashRe.ReplaceAllString(raw, "/")
	dirIntent := len(raw) > 1 && strings.HasSuffix(raw, "/")
	raw = strings.TrimRight(raw, "/")
	if raw == "" {
		raw = "/"
	}

	metaIdx := metaRe.FindStringIndex(raw)
	head, tail := raw, ""
	if metaIdx != nil {
		head = raw[:metaIdx[0]]
		tail = raw[metaIdx[0]:]
	}
	cut := strings.LastIndex(head, "/")
	headDir, headRest := "", head
	if cut != -1 {
		headDir = head[:cut]
		if headDir == "" {
			headDir = "/"
		}
		headRest = head[cut+1:]
	}

	target := headDir
	if target == "" {
		target = "."
	}
	absDir := resolve(cwd, target)
	relDir, err := filepath.Rel(root, absDir)
	if err != nil {
		return zero, errs.New("E_PATH_INVALID", "Path escapes the workspace: "+input).
			WithHint("Claim paths inside the repository only.")
	}
	relDir = filepath.ToSlash(relDir)
	if relDir == ".." || strings.HasPrefix(relDir, "../") {
		return zero, errs.New("E_PATH_INVALID", "Path escapes the workspace: "+input).
			WithHint("Claim paths inside the repository only.")
	}
	segments := []string{}
	for _, p := range []string{relDir, headRest + tail} {
		if p != "" && p != "." {
			segments = append(segments, p)
		}
	}
	pattern := strings.Join(segments, "/")
	if pattern == "" || pattern == "." {
		return zero, errs.New("E_PATH_INVALID", "Whole-workspace claims need --confirm-global.").
			WithHint(`fridge claim "src/**" is usually what you want.`)
	}
	for _, seg := range strings.Split(pattern, "/") {
		if seg == "." || seg == ".." {
			return zero, errs.New("E_PATH_INVALID", "Path traversal is not allowed: "+input)
		}
		if winReservedRe.MatchString(seg) {
			return zero, errs.New("E_PATH_INVALID", "Reserved Windows name: "+seg)
		}
		if strings.Contains(seg, ":") {
			return zero, errs.New("E_PATH_INVALID", "':' is not allowed in a path segment: "+seg)
		}
		if strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ") {
			return zero, errs.New("E_PATH_INVALID", "Segment must not end with a dot or space: "+seg)
		}
	}
	firstSeg := strings.Split(pattern, "/")[0]
	for _, r := range reservedRoots {
		if firstSeg == r {
			return zero, errs.New("E_PATH_INVALID", firstSeg+"/ is reserved and cannot be claimed.")
		}
	}
	if err := checkSymlinkContainment(pattern, root, input); err != nil {
		return zero, err
	}
	return Normalized{Pattern: pattern, DirIntent: dirIntent, IsGlob: metaRe.MatchString(pattern)}, nil
}

// resolve is path.resolve(base, target) for the one-argument case this code
// needs: an absolute target wins, a relative one is joined onto base.
func resolve(base, target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	if isWindows && strings.HasPrefix(target, "/") {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(base, target))
}

// checkSymlinkContainment resolves the deepest ancestor of the pattern that
// actually exists. Probing the deepest existing ancestor, rather than the whole
// literal prefix, catches a symlinked parent directory and a dangling symlink,
// both of which a naive check misses.
func checkSymlinkContainment(pattern, root, input string) error {
	lp := LiteralPrefix(pattern)
	if lp == "" {
		lp = strings.Split(pattern, "/")[0]
	}
	probe := filepath.Join(root, filepath.FromSlash(lp))
	for probe != root && !lexists(probe) {
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	if probe == root || !lexists(probe) {
		return nil
	}
	real, err := filepath.EvalSymlinks(probe)
	if err != nil {
		// A dangling symlink: resolve one hop by hand rather than trusting it.
		if link, lerr := os.Readlink(probe); lerr == nil {
			real = resolve(filepath.Dir(probe), link)
		} else {
			real = probe
		}
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return errs.New("E_PATH_INVALID", "Symlink escapes the workspace: "+input).
			WithHint("Claims must stay inside the repository. Resolve the real path and claim that.")
	}
	return nil
}

func lexists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// IsGlobal reports a pattern that names the whole workspace.
func IsGlobal(pattern string) bool {
	return pattern == "**" || pattern == "*" || pattern == "." || pattern == "/"
}

// IsRootGlobal reports a pattern that can reach any depth from the root.
func IsRootGlobal(p string) bool {
	return p == "**" || p == "*" || p == "**/*" || strings.HasPrefix(p, "**/")
}

// Listing is the set of files a scope was materialized against.
type Listing struct {
	Files        []string
	Materializer string
}

// ListWorkspaceFiles prefers git, which already knows about .gitignore, and
// falls back to a bounded walk when git is absent or this is not a checkout.
func ListWorkspaceFiles(root string) Listing {
	cmd := exec.Command("git", "-C", root, "ls-files", "-co", "--exclude-standard", "-z")
	out, err := cmd.Output()
	if err == nil {
		files := []string{}
		for _, f := range strings.Split(string(out), "\x00") {
			if f != "" {
				files = append(files, f)
			}
		}
		return Listing{Files: files, Materializer: "git"}
	}
	skip := map[string]bool{".git": true, ".fridge": true, "node_modules": true, ".venv": true, "dist": true, "build": true, ".next": true}
	files := []string{}
	var walk func(dir, rel string)
	walk = func(dir, rel string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if skip[e.Name()] {
				continue
			}
			child := filepath.Join(dir, e.Name())
			childRel := e.Name()
			if rel != "" {
				childRel = rel + "/" + e.Name()
			}
			if e.IsDir() {
				walk(child, childRel)
			} else if e.Type().IsRegular() {
				files = append(files, childRel)
			}
		}
	}
	walk(root, "")
	return Listing{Files: files, Materializer: "walk"}
}

// Scope is a claim's set of patterns plus the concrete files they cover.
type Scope struct {
	Include      []string
	Exclude      []string
	Materialized []string
	Truncated    bool
	Matchers     []string
	Materializer string
}

// MaterializeOptions tunes Materialize; Files short-circuits the listing so a
// scope can be narrowed against an already-computed set.
type MaterializeOptions struct {
	Limit       int
	Files       []string
	Insensitive bool
}

// Materialize expands patterns into concrete repo-relative files, plus the
// matchers a later overlap test uses.
func Materialize(root string, patterns []string, opts MaterializeOptions) (Scope, error) {
	listing := Listing{Files: opts.Files, Materializer: "given"}
	if opts.Files == nil {
		listing = ListWorkspaceFiles(root)
	}
	matchers := []string{}
	for _, p := range patterns {
		matchers = append(matchers, p)
		if !metaRe.MatchString(p) {
			abs := filepath.Join(root, filepath.FromSlash(p))
			if st, err := os.Stat(abs); err == nil && st.IsDir() {
				matchers = append(matchers, p+"/**")
			}
		}
	}
	compiled, err := CompileMatchers(matchers, opts.Insensitive)
	if err != nil {
		return Scope{}, err
	}
	out := []string{}
	truncated := false
	limit := opts.Limit
	if limit <= 0 {
		limit = 5000
	}
	for _, f := range listing.Files {
		if compiled.Match(f) {
			if len(out) >= limit {
				truncated = true
				break
			}
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return Scope{
		Include:      patterns,
		Materialized: out,
		Truncated:    truncated,
		Matchers:     matchers,
		Materializer: listing.Materializer,
	}, nil
}

// Overlap is the answer to "can these two scopes touch the same file", plus the
// evidence a human needs to understand the refusal.
type Overlap struct {
	Overlap bool
	Reason  string
	Paths   []string
}

// ScopesOverlap is the conservative overlap test of section 6.3. Overlap is
// decided on the patterns themselves, so a pair that can only collide on a file
// that does not exist yet is still refused. Materialization is consulted only
// to name the concrete files in the error.
func ScopesOverlap(a, b Scope, insensitive bool) (Overlap, error) {
	fold := func(s string) string { return CaseFold(s, insensitive) }
	excludedBy := func(scope Scope, pattern string) (bool, error) {
		for _, e := range scope.Exclude {
			ok, err := PatternCovers(e, pattern, insensitive)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	skip := func(pa, pb string) (bool, error) {
		// An exclude only rules a pair out when it swallows the other side
		// whole. Anything less and some path could still satisfy both.
		ea, err := excludedBy(a, pb)
		if err != nil {
			return false, err
		}
		if ea {
			return true, nil
		}
		return excludedBy(b, pa)
	}

	for _, pa := range a.Include {
		for _, pb := range b.Include {
			if !IsRootGlobal(pa) && !IsRootGlobal(pb) {
				continue
			}
			drop, err := skip(pa, pb)
			if err != nil {
				return Overlap{}, err
			}
			if drop {
				continue
			}
			return Overlap{true, "global-pattern", []string{pa, pb}}, nil
		}
	}

	setB := map[string]bool{}
	for _, f := range b.Materialized {
		setB[fold(f)] = true
	}
	hits := []string{}
	for _, f := range a.Materialized {
		if setB[fold(f)] {
			hits = append(hits, f)
		}
	}

	for _, pa := range a.Include {
		for _, pb := range b.Include {
			drop, err := skip(pa, pb)
			if err != nil {
				return Overlap{}, err
			}
			if drop {
				continue
			}
			for _, wa := range withSubtree(pa) {
				for _, wb := range withSubtree(pb) {
					can, err := PatternsCanIntersect(wa, wb, insensitive)
					if err != nil {
						return Overlap{}, err
					}
					if !can {
						continue
					}
					if len(hits) > 0 {
						return Overlap{true, "materialized-intersection", capPaths(hits)}, nil
					}
					if fold(pa) == fold(pb) {
						return Overlap{true, "same-pattern", []string{pa}}, nil
					}
					la := fold(LiteralPrefix(pa))
					lb := fold(LiteralPrefix(pb))
					if la != "" && lb != "" && (IsPrefixPath(la, lb) || IsPrefixPath(lb, la)) {
						return Overlap{true, "literal-prefix-nesting", []string{pa, pb}}, nil
					}
					return Overlap{true, "pattern-intersection", []string{pa, pb}}, nil
				}
			}
		}
	}
	return Overlap{Overlap: false}, nil
}

func capPaths(in []string) []string {
	if len(in) > 25 {
		return in[:25]
	}
	return in
}
