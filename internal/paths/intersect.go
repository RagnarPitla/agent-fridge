// SPDX-License-Identifier: Apache-2.0
package paths

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/RagnarPitla/agent-fridge/internal/errs"
)

func expandBracesBounded(pattern string) ([]string, error) {
	return ExpandBraces(pattern)
}

type tokKind int

const (
	tokLit tokKind = iota
	tokAny
	tokStar
	tokClass
)

type segTok struct {
	kind   tokKind
	ch     rune
	neg    bool
	ranges []charRange
}

type charRange struct{ lo, hi rune }

func normalizeRanges(in []charRange) []charRange {
	sort.Slice(in, func(i, j int) bool {
		if in[i].lo == in[j].lo {
			return in[i].hi < in[j].hi
		}
		return in[i].lo < in[j].lo
	})
	out := []charRange{}
	for _, r := range in {
		if len(out) == 0 || r.lo > out[len(out)-1].hi+1 {
			out = append(out, r)
		} else if r.hi > out[len(out)-1].hi {
			out[len(out)-1].hi = r.hi
		}
	}
	return out
}

func parseClassRanges(set []rune, pattern string) ([]charRange, error) {
	out := []charRange{}
	for i := 0; i < len(set); {
		if i+2 < len(set) && set[i+1] == '-' {
			if set[i] > set[i+2] {
				return nil, errs.New("E_PATH_INVALID", "Descending character range in: "+pattern)
			}
			out = append(out, charRange{set[i], set[i+2]})
			i += 3
		} else {
			out = append(out, charRange{set[i], set[i]})
			i++
		}
	}
	return normalizeRanges(out), nil
}

// segTokens turns one path segment into single-character matchers plus stars.
func segTokens(seg, pattern string) ([]segTok, error) {
	chars := []rune(seg)
	var out []segTok
	for i := 0; i < len(chars); i++ {
		c := chars[i]
		switch c {
		case '*':
			if len(out) == 0 || out[len(out)-1].kind != tokStar {
				out = append(out, segTok{kind: tokStar})
			}
		case '?':
			out = append(out, segTok{kind: tokAny})
		case '[':
			j := i + 1
			neg := false
			if j < len(chars) && (chars[j] == '!' || chars[j] == '^') {
				neg = true
				j++
			}
			set := []rune{}
			for j < len(chars) && chars[j] != ']' {
				set = append(set, chars[j])
				j++
			}
			if j >= len(chars) {
				return nil, errs.New("E_PATH_INVALID", "Unterminated character class in: "+pattern)
			}
			if len(set) == 0 {
				return nil, errs.New("E_PATH_INVALID", "Empty character class in: "+pattern)
			}
			ranges, err := parseClassRanges(set, pattern)
			if err != nil {
				return nil, err
			}
			out = append(out, segTok{kind: tokClass, neg: neg, ranges: ranges})
			i = j
		default:
			out = append(out, segTok{kind: tokLit, ch: c})
		}
	}
	return out, nil
}

func rangeHas(ranges []charRange, c rune) bool {
	for _, r := range ranges {
		if c >= r.lo && c <= r.hi {
			return true
		}
	}
	return false
}

// classHas reports membership in a character class, including inclusive
// ranges such as a-z.
func classHas(t segTok, c rune, insensitive bool) bool {
	has := rangeHas(t.ranges, c)
	if !has && insensitive {
		for folded := unicode.SimpleFold(c); folded != c; folded = unicode.SimpleFold(folded) {
			if rangeHas(t.ranges, folded) {
				has = true
				break
			}
		}
	}
	if t.neg {
		return !has
	}
	return has
}

func sameChar(a, b rune, insensitive bool) bool {
	if a == b {
		return true
	}
	return insensitive && strings.EqualFold(string(a), string(b))
}

func foldedRanges(in []charRange, insensitive bool) ([]charRange, bool) {
	if !insensitive {
		return normalizeRanges(append([]charRange{}, in...)), true
	}
	out := []charRange{}
	add := func(lo, hi rune) {
		if lo <= hi {
			out = append(out, charRange{lo, hi})
		}
	}
	for _, r := range in {
		if r.lo < 0 || r.hi > unicode.MaxASCII {
			return nil, false
		}
		add(r.lo, minRune(r.hi, '@'))
		add(maxRune(r.lo, 'A')+('a'-'A'), minRune(r.hi, 'Z')+('a'-'A'))
		add(maxRune(r.lo, '['), r.hi)
	}
	return normalizeRanges(out), true
}

func rangesIntersect(a, b []charRange) bool {
	for _, x := range a {
		for _, y := range b {
			if maxRune(x.lo, y.lo) <= minRune(x.hi, y.hi) {
				return true
			}
		}
	}
	return false
}

func hasOutside(positive, excluded []charRange) bool {
	for _, p := range positive {
		cursor := p.lo
		covered := false
		for _, x := range excluded {
			if x.hi < cursor {
				continue
			}
			if x.lo > p.hi {
				break
			}
			if x.lo > cursor {
				return true
			}
			if x.hi >= p.hi {
				covered = true
				break
			}
			if x.hi+1 > cursor {
				cursor = x.hi + 1
			}
			if cursor > p.hi {
				break
			}
		}
		if !covered && cursor <= p.hi {
			return true
		}
	}
	return false
}

func minRune(a, b rune) rune {
	if a < b {
		return a
	}
	return b
}

func maxRune(a, b rune) rune {
	if a > b {
		return a
	}
	return b
}

// charsIntersect reports whether two single-character matchers can accept the
// same character.
func charsIntersect(x, y segTok, insensitive bool) bool {
	if x.kind == tokAny || y.kind == tokAny {
		return true
	}
	if x.kind == tokLit && y.kind == tokLit {
		return sameChar(x.ch, y.ch, insensitive)
	}
	if x.kind == tokLit {
		return classHas(y, x.ch, insensitive)
	}
	if y.kind == tokLit {
		return classHas(x, y.ch, insensitive)
	}
	xr, xKnown := foldedRanges(x.ranges, insensitive)
	yr, yKnown := foldedRanges(y.ranges, insensitive)
	if !xKnown || !yKnown {
		return true
	}
	if x.neg && y.neg {
		return true
	}
	if x.neg {
		return hasOutside(yr, xr)
	}
	if y.neg {
		return hasOutside(xr, yr)
	}
	return rangesIntersect(xr, yr)
}

func allStars(ts []segTok) bool {
	for _, t := range ts {
		if t.kind != tokStar {
			return false
		}
	}
	return true
}

// segmentsIntersect decides whether two brace-free, separator-free segment
// patterns can match a common string.
func segmentsIntersect(sa, sb string, insensitive bool, pattern string) (bool, error) {
	a, err := segTokens(sa, pattern)
	if err != nil {
		return false, err
	}
	b, err := segTokens(sb, pattern)
	if err != nil {
		return false, err
	}
	memo := make(map[int]bool, (len(a)+1)*(len(b)+1))
	var go_ func(i, j int) bool
	go_ = func(i, j int) bool {
		key := i*(len(b)+1) + j
		if v, ok := memo[key]; ok {
			return v
		}
		var res bool
		switch {
		case i == len(a) && j == len(b):
			res = true
		case i == len(a):
			res = allStars(b[j:])
		case j == len(b):
			res = allStars(a[i:])
		case a[i].kind == tokStar:
			res = go_(i+1, j) || go_(i, j+1)
		case b[j].kind == tokStar:
			res = go_(i, j+1) || go_(i+1, j)
		default:
			res = charsIntersect(a[i], b[j], insensitive) && go_(i+1, j+1)
		}
		memo[key] = res
		return res
	}
	return go_(0, 0), nil
}

func allGlobstars(segs []string) bool {
	for _, s := range segs {
		if s != "**" {
			return false
		}
	}
	return true
}

// globsIntersect decides whether two brace-free patterns can match a common
// path. `**` spans zero or more whole segments.
func globsIntersect(pa, pb string, insensitive bool) (bool, error) {
	a := strings.Split(pa, "/")
	b := strings.Split(pb, "/")
	memo := make(map[int]bool, (len(a)+1)*(len(b)+1))
	label := pa + " vs " + pb
	var firstErr error
	var go_ func(i, j int) bool
	go_ = func(i, j int) bool {
		key := i*(len(b)+1) + j
		if v, ok := memo[key]; ok {
			return v
		}
		var res bool
		switch {
		case i == len(a) && j == len(b):
			res = true
		case i == len(a):
			res = allGlobstars(b[j:])
		case j == len(b):
			res = allGlobstars(a[i:])
		case a[i] == "**" && b[j] == "**":
			res = go_(i+1, j+1) || go_(i+1, j) || go_(i, j+1)
		case a[i] == "**":
			res = go_(i+1, j) || go_(i, j+1)
		case b[j] == "**":
			res = go_(i, j+1) || go_(i+1, j)
		default:
			ok, err := segmentsIntersect(a[i], b[j], insensitive, label)
			if err != nil && firstErr == nil {
				firstErr = err
			}
			res = ok && go_(i+1, j+1)
		}
		memo[key] = res
		return res
	}
	out := go_(0, 0)
	if firstErr != nil {
		return false, firstErr
	}
	return out, nil
}

// PatternsCanIntersect answers "could any path, existing or not, match both
// patterns?".
//
// It never answers no when a common path exists. It may answer yes for a pair
// that would not have collided in practice, which costs a claim and protects
// the file.
func PatternsCanIntersect(pa, pb string, insensitive bool) (bool, error) {
	ea, err := expandBracesBounded(pa)
	if err != nil {
		return false, err
	}
	eb, err := expandBracesBounded(pb)
	if err != nil {
		return false, err
	}
	for _, x := range ea {
		for _, y := range eb {
			ok, err := globsIntersect(x, y, insensitive)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
	}
	return false, nil
}

// PatternCovers reports whether every path matching inner also matches outer.
// Sufficient, not complete: it recognises the shapes an --exclude actually
// takes and says no whenever unsure, which keeps the caller failing closed.
func PatternCovers(outer, inner string, insensitive bool) (bool, error) {
	covers1 := func(o, i string) bool {
		if CaseFold(o, insensitive) == CaseFold(i, insensitive) {
			return true
		}
		if !strings.HasSuffix(o, "/**") {
			return false
		}
		base := strings.TrimSuffix(o, "/**")
		if metaRe.MatchString(base) {
			return false
		}
		fb := CaseFold(base, insensitive)
		fi := CaseFold(i, insensitive)
		return fi == fb || strings.HasPrefix(fi, fb+"/")
	}
	inners, err := expandBracesBounded(inner)
	if err != nil {
		return false, err
	}
	outers, err := expandBracesBounded(outer)
	if err != nil {
		return false, err
	}
	for _, ei := range inners {
		found := false
		for _, eo := range outers {
			if covers1(eo, ei) {
				found = true
				break
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}

// withSubtree reflects that a claim on a path also owns everything under it,
// once that path becomes a directory.
func withSubtree(pattern string) []string {
	if strings.HasSuffix(pattern, "/**") {
		return []string{pattern}
	}
	return []string{pattern, pattern + "/**"}
}

// ResolveInsideWorkspace resolves a user-supplied output path and proves it
// lands inside the workspace, symlinks included. A generated view written
// outside the repository is an arbitrary-file-write with extra steps.
func ResolveInsideWorkspace(root, input, what string) (string, error) {
	if strings.TrimSpace(input) == "" {
		return "", errs.New("E_PATH_INVALID", "Empty "+what+".")
	}
	if strings.ContainsRune(input, 0) {
		return "", errs.New("E_PATH_INVALID", what+" contains a NUL byte.")
	}
	abs := input
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, input)
	}
	abs = filepath.Clean(abs)
	inside := func(target, base string) bool {
		rel, err := filepath.Rel(base, target)
		if err != nil {
			return false
		}
		return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = filepath.Clean(root)
	}
	if !inside(abs, absRoot) {
		return "", errs.New("E_PATH_INVALID", what+" escapes the workspace: "+input).
			WithHint("Generated views must stay inside the repository.")
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		realRoot = absRoot
	}
	// Walk up to the deepest ancestor that exists: the file itself usually
	// does not yet, but a symlinked directory on the way there decides where
	// it lands.
	probe := abs
	for {
		if _, err := os.Lstat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	realProbe, err := filepath.EvalSymlinks(probe)
	if err != nil {
		realProbe = probe
	}
	tail, err := filepath.Rel(probe, abs)
	if err != nil {
		tail = ""
	}
	realTarget := realProbe
	if tail != "" && tail != "." {
		realTarget = filepath.Join(realProbe, tail)
	}
	if !inside(realTarget, realRoot) {
		return "", errs.New("E_PATH_INVALID", what+" resolves through a symlink to outside the workspace: "+input).
			WithHint("Write generated views to a real path inside the repository.")
	}
	return abs, nil
}

// ContainedFiles drops anything whose real path leaves the workspace, however
// it got matched, and reports what was dropped.
func ContainedFiles(root string, files []string) (kept, escaped []string) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		absRoot = filepath.Clean(root)
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		realRoot = absRoot
	}
	kept = []string{}
	escaped = []string{}
	for _, f := range files {
		real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(f)))
		if err != nil {
			kept = append(kept, f)
			continue
		}
		rel, err := filepath.Rel(realRoot, real)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			escaped = append(escaped, f)
			continue
		}
		kept = append(kept, f)
	}
	return kept, escaped
}
