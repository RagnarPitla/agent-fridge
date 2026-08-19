// SPDX-License-Identifier: Apache-2.0
// Deterministic JSON that is byte-identical to the Node reference implementation.
//
// src/core/util.mjs writes every record with stableStringify: JSON.stringify of
// a value whose object keys have been sorted, two-space indent, trailing
// newline. Matching that exactly is what lets a .fridge/ written by this binary
// be read, rewritten and diffed by the Node CLI without churn, so this file
// reimplements JSON.stringify's number formatting and string escaping rather
// than leaning on encoding/json, whose HTML escaping and float formatting
// differ.
package jsonx

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Obj is a JSON object. Keys are sorted on the way out, never on the way in.
type Obj map[string]any

// Arr is a JSON array.
type Arr []any

// Get walks a dotted path and returns nil when any hop is missing.
func (o Obj) Get(dotted string) any {
	var cur any = o
	for _, k := range strings.Split(dotted, ".") {
		m, ok := cur.(Obj)
		if !ok {
			return nil
		}
		cur, ok = m[k]
		if !ok {
			return nil
		}
	}
	return cur
}

// Str returns a string field, or "" when it is missing or another type.
func (o Obj) Str(dotted string) string {
	s, _ := o.Get(dotted).(string)
	return s
}

// Num returns a numeric field, or 0 when it is missing or another type.
func (o Obj) Num(dotted string) float64 {
	f, _ := o.Get(dotted).(float64)
	return f
}

// Int returns a numeric field truncated to an int.
func (o Obj) Int(dotted string) int { return int(o.Num(dotted)) }

// Bool returns a boolean field, or false when it is missing or another type.
func (o Obj) Bool(dotted string) bool {
	b, _ := o.Get(dotted).(bool)
	return b
}

// ObjAt returns a nested object, or nil.
func (o Obj) ObjAt(dotted string) Obj {
	m, _ := o.Get(dotted).(Obj)
	return m
}

// ArrAt returns a nested array, or nil.
func (o Obj) ArrAt(dotted string) Arr {
	a, _ := o.Get(dotted).(Arr)
	return a
}

// Strings returns a nested array as a []string, skipping non-strings.
func (o Obj) Strings(dotted string) []string {
	out := []string{}
	for _, v := range o.ArrAt(dotted) {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Clone deep-copies an object so a rewrite never mutates a shared record.
func (o Obj) Clone() Obj {
	out := make(Obj, len(o))
	for k, v := range o {
		out[k] = cloneValue(v)
	}
	return out
}

// With returns a copy with the given fields replaced, the Go spelling of the
// `{ ...claim, state: 'released' }` idiom the Node code uses everywhere.
func (o Obj) With(fields Obj) Obj {
	out := o.Clone()
	for k, v := range fields {
		out[k] = Normalize(v)
	}
	return out
}

func cloneValue(v any) any {
	switch t := v.(type) {
	case Obj:
		return t.Clone()
	case Arr:
		out := make(Arr, len(t))
		for i, e := range t {
			out[i] = cloneValue(e)
		}
		return out
	default:
		return v
	}
}

// Set writes a value at a dotted path. It reports an error when an intermediate
// hop is missing or is not an object, which is what `fridge config a.b c`
// surfaces as E_NOT_FOUND.
func (o Obj) Set(dotted string, value any) error {
	keys := strings.Split(dotted, ".")
	cur := o
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(Obj)
		if !ok {
			return errors.New("no config section '" + k + "'")
		}
		cur = next
	}
	cur[keys[len(keys)-1]] = Normalize(value)
	return nil
}

// Normalize converts ordinary Go values into the small JSON value model, so
// callers can write Obj{"pid": 42, "include": []string{"src/**"}} directly.
func Normalize(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case bool, string, float64:
		return t
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case float32:
		return float64(t)
	case Obj:
		out := make(Obj, len(t))
		for k, e := range t {
			out[k] = Normalize(e)
		}
		return out
	case Arr:
		out := make(Arr, len(t))
		for i, e := range t {
			out[i] = Normalize(e)
		}
		return out
	case []string:
		out := make(Arr, len(t))
		for i, e := range t {
			out[i] = e
		}
		return out
	case []Obj:
		out := make(Arr, len(t))
		for i, e := range t {
			out[i] = Normalize(e)
		}
		return out
	case []any:
		out := make(Arr, len(t))
		for i, e := range t {
			out[i] = Normalize(e)
		}
		return out
	case map[string]any:
		out := make(Obj, len(t))
		for k, e := range t {
			out[k] = Normalize(e)
		}
		return out
	default:
		return v
	}
}

// Stable renders a value the way stableStringify does: sorted keys, two-space
// indent, trailing newline.
func Stable(v any) string {
	var sb strings.Builder
	writeValue(&sb, Normalize(v), 2, 0, nil, "")
	sb.WriteByte('\n')
	return sb.String()
}

// Ordering supplies an explicit key order for the object at a dotted path.
// Returning nil for a path falls back to sorted order.
type Ordering func(path string) []string

// Indent renders a value the way JSON.stringify(value, null, 2) does, with no
// trailing newline and with the key order the Ordering supplies. It exists so
// that human-facing text can reproduce the Node writer's insertion order.
func Indent(v any, ord Ordering) string {
	var sb strings.Builder
	writeValue(&sb, Normalize(v), 2, 0, ord, "")
	return sb.String()
}

// Compact renders a value with no indentation and no trailing newline, the
// equivalent of JSON.stringify(value) after key sorting.
func Compact(v any) string {
	var sb strings.Builder
	writeValue(&sb, Normalize(v), 0, 0, nil, "")
	return sb.String()
}

func writeValue(sb *strings.Builder, v any, indent, depth int, ord Ordering, path string) {
	switch t := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		if t {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case string:
		writeString(sb, t)
	case float64:
		sb.WriteString(FormatNumber(t))
	case Obj:
		writeObject(sb, t, indent, depth, ord, path)
	case Arr:
		writeArray(sb, t, indent, depth, ord, path)
	default:
		sb.WriteString("null")
	}
}

func pad(sb *strings.Builder, indent, depth int) {
	if indent > 0 {
		sb.WriteByte('\n')
		sb.WriteString(strings.Repeat(" ", indent*depth))
	}
}

func writeObject(sb *strings.Builder, o Obj, indent, depth int, ord Ordering, path string) {
	if len(o) == 0 {
		sb.WriteString("{}")
		return
	}
	keys := make([]string, 0, len(o))
	for k := range o {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return LessUTF16(keys[i], keys[j]) })
	if ord != nil {
		if want := ord(path); want != nil {
			seen := map[string]bool{}
			front := []string{}
			for _, k := range want {
				if _, ok := o[k]; ok && !seen[k] {
					seen[k] = true
					front = append(front, k)
				}
			}
			for _, k := range keys {
				if !seen[k] {
					front = append(front, k)
				}
			}
			keys = front
		}
	}
	sb.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		pad(sb, indent, depth+1)
		writeString(sb, k)
		sb.WriteByte(':')
		if indent > 0 {
			sb.WriteByte(' ')
		}
		child := k
		if path != "" {
			child = path + "." + k
		}
		writeValue(sb, o[k], indent, depth+1, ord, child)
	}
	pad(sb, indent, depth)
	sb.WriteByte('}')
}

func writeArray(sb *strings.Builder, a Arr, indent, depth int, ord Ordering, path string) {
	if len(a) == 0 {
		sb.WriteString("[]")
		return
	}
	sb.WriteByte('[')
	for i, e := range a {
		if i > 0 {
			sb.WriteByte(',')
		}
		pad(sb, indent, depth+1)
		writeValue(sb, e, indent, depth+1, ord, path)
	}
	pad(sb, indent, depth)
	sb.WriteByte(']')
}

const hexDigits = "0123456789abcdef"

// writeString escapes exactly what JSON.stringify escapes: the quote, the
// backslash, and control characters. Everything else, including non-ASCII, is
// emitted raw, because that is what the Node writer produces.
func writeString(sb *strings.Builder, s string) {
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			sb.WriteString("\\\"")
		case c == '\\':
			sb.WriteString("\\\\")
		case c == '\b':
			sb.WriteString("\\b")
		case c == '\f':
			sb.WriteString("\\f")
		case c == '\n':
			sb.WriteString("\\n")
		case c == '\r':
			sb.WriteString("\\r")
		case c == '\t':
			sb.WriteString("\\t")
		case c < 0x20:
			sb.WriteString("\\u00")
			sb.WriteByte(hexDigits[c>>4])
			sb.WriteByte(hexDigits[c&0xf])
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
}

// FormatNumber implements the ECMAScript Number::toString algorithm, which is
// what JSON.stringify uses. Go's %g would print 1e+21 as 1e+21 but 100000 as
// 100000 with different thresholds, so the rules are spelled out here.
func FormatNumber(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	if f == 0 {
		return "0"
	}
	neg := false
	if f < 0 {
		neg = true
		f = -f
	}
	sci := strconv.FormatFloat(f, 'e', -1, 64)
	epos := strings.IndexByte(sci, 'e')
	digits := strings.Replace(sci[:epos], ".", "", 1)
	exp, _ := strconv.Atoi(sci[epos+1:])
	k := len(digits)
	n := exp + 1
	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		mant := digits
		if k > 1 {
			mant = digits[:1] + "." + digits[1:]
		}
		if n-1 >= 0 {
			out = mant + "e+" + strconv.Itoa(n-1)
		} else {
			out = mant + "e-" + strconv.Itoa(1-n)
		}
	}
	if neg {
		out = "-" + out
	}
	return out
}

// LessUTF16 orders strings the way Array.prototype.sort does, by UTF-16 code
// unit. For the ASCII keys this protocol uses it is plain byte order; the slow
// path only runs when a record carries a non-ASCII key.
func LessUTF16(a, b string) bool {
	if isASCII(a) && isASCII(b) {
		return a < b
	}
	au := utf16.Encode([]rune(a))
	bu := utf16.Encode([]rune(b))
	for i := 0; i < len(au) && i < len(bu); i++ {
		if au[i] != bu[i] {
			return au[i] < bu[i]
		}
	}
	return len(au) < len(bu)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Parse decodes JSON text into the value model. Numbers become float64, which
// is exactly the precision JSON.parse gives the Node implementation.
func Parse(data []byte) (any, error) {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	// JSON.parse rejects trailing content; so does this.
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		return nil, errors.New("unexpected trailing content")
	}
	return convert(raw)
}

// ParseObj decodes JSON text that must be an object.
func ParseObj(data []byte) (Obj, error) {
	v, err := Parse(data)
	if err != nil {
		return nil, err
	}
	o, ok := v.(Obj)
	if !ok {
		return nil, errors.New("expected a JSON object")
	}
	return o, nil
}

func convert(v any) (any, error) {
	switch t := v.(type) {
	case nil, bool, string:
		return t, nil
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return nil, err
		}
		return f, nil
	case map[string]any:
		out := make(Obj, len(t))
		for k, e := range t {
			c, err := convert(e)
			if err != nil {
				return nil, err
			}
			out[k] = c
		}
		return out, nil
	case []any:
		out := make(Arr, len(t))
		for i, e := range t {
			c, err := convert(e)
			if err != nil {
				return nil, err
			}
			out[i] = c
		}
		return out, nil
	default:
		return nil, errors.New("unsupported JSON value")
	}
}

// DeepMerge layers `over` on top of `base`, recursing into objects and
// replacing arrays wholesale. It matches the deepMerge in src/core/store.mjs.
func DeepMerge(base, over Obj) Obj {
	out := base.Clone()
	for k, v := range over {
		if ov, ok := v.(Obj); ok {
			if bv, ok := out[k].(Obj); ok {
				out[k] = DeepMerge(bv, ov)
				continue
			}
		}
		out[k] = cloneValue(Normalize(v))
	}
	return out
}
