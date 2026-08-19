// SPDX-License-Identifier: Apache-2.0
package jsonx

import (
	"strings"
	"testing"
)

// stableStringify is the byte-compatibility contract with the Node side:
// keys sorted lexicographically at every depth, two-space indent, trailing
// newline. If this drifts, .fridge/ written by Go stops round-tripping.
func TestStableSortsKeysAtEveryDepth(t *testing.T) {
	v := Obj{
		"z": float64(1),
		"a": Obj{"nested": Obj{"y": true, "b": nil}},
		"m": Arr{Obj{"q": "1", "p": "2"}},
	}
	got := Stable(v)
	want := strings.Join([]string{
		"{",
		`  "a": {`,
		`    "nested": {`,
		`      "b": null,`,
		`      "y": true`,
		"    }",
		"  },",
		`  "m": [`,
		"    {",
		`      "p": "2",`,
		`      "q": "1"`,
		"    }",
		"  ],",
		`  "z": 1`,
		"}",
		"",
	}, "\n")
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestStableDropsUndefinedLikeNodeDoes(t *testing.T) {
	// Node's JSON.stringify omits keys whose value is undefined. The Go port
	// models that by deleting the key, so a nil interface must still emit null.
	got := Stable(Obj{"a": nil, "b": float64(2)})
	if got != "{\n  \"a\": null,\n  \"b\": 2\n}\n" {
		t.Errorf("explicit null must survive, got %q", got)
	}
}

func TestNumberFormattingMatchesECMAScript(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{-0, "0"},
		{1.5, "1.5"},
		{1e21, "1e+21"},
		{1e-7, "1e-7"},
		{123456789012345680000, "123456789012345680000"},
		{0.1, "0.1"},
		{-2.5, "-2.5"},
	}
	for _, c := range cases {
		if got := FormatNumber(c.in); got != c.want {
			t.Errorf("FormatNumber(%v) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestStringsAreNotHTMLEscaped(t *testing.T) {
	got := Compact(Obj{"a": "<b>&\"quoted\"\n\t"})
	want := `{"a":"<b>&\"quoted\"\n\t"}`
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
}

func TestSortIsUTF16CodeUnitOrder(t *testing.T) {
	// JavaScript sorts by UTF-16 code unit, which puts an astral plane
	// character (surrogate pair, 0xD800..) before U+FFFD.
	if !LessUTF16("A", "a") || LessUTF16("b", "a") {
		t.Errorf("basic ASCII ordering is wrong")
	}
	if !LessUTF16("\U0001F600", "\uFFFD") {
		t.Errorf("surrogate pairs must sort before U+FFFD, as in JavaScript")
	}
}

func TestRoundTripThroughParse(t *testing.T) {
	src := `{"b":[1,2,{"c":null}],"a":"x","d":true}`
	v, err := ParseObj([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := Compact(v); got != `{"a":"x","b":[1,2,{"c":null}],"d":true}` {
		t.Errorf("round trip produced %s", got)
	}
	if v.Str("a") != "x" || !v.Bool("d") {
		t.Errorf("accessors disagree with the parsed document")
	}
	list := v.ArrAt("b")
	if len(list) != 3 || list[0] != float64(1) {
		t.Errorf("ArrAt returned %v", list)
	}
	last, ok := list[2].(Obj)
	if !ok || last["c"] != nil {
		t.Errorf("nested null should read back as nil, got %v", list[2])
	}
}

func TestDeepMergeOverlaysObjectsAndReplacesScalars(t *testing.T) {
	base := Obj{"a": Obj{"x": float64(1), "y": float64(2)}, "list": Arr{float64(1)}}
	over := Obj{"a": Obj{"y": float64(9), "z": float64(3)}, "list": Arr{float64(2), float64(3)}}
	got := Compact(DeepMerge(base, over))
	want := `{"a":{"x":1,"y":9,"z":3},"list":[2,3]}`
	if got != want {
		t.Errorf("got %s want %s", got, want)
	}
	if base.Num("a.y") != 2 {
		t.Errorf("DeepMerge must not mutate its inputs")
	}
}

func TestIndentPreservesGivenOrdering(t *testing.T) {
	v := Obj{"second": float64(2), "first": float64(1)}
	got := Indent(v, func(path string) []string {
		if path == "" {
			return []string{"first", "second"}
		}
		return nil
	})
	if !strings.HasPrefix(got, "{\n  \"first\": 1,\n  \"second\": 2") {
		t.Errorf("Indent ignored the requested key order:\n%s", got)
	}
}
