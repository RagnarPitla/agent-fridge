// SPDX-License-Identifier: Apache-2.0
// Identifiers, hashes, durations and the small helpers that every record needs.
// Ported from src/core/util.mjs; the ULID alphabet, the slug rules and the
// duration grammar all have to agree with it byte for byte.
package util

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	mrand "math/rand"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/errs"
)

const b32 = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// IDPattern is the shape every generated identifier has to keep.
var IDPattern = regexp.MustCompile(`^(wsp|act|ses|clm|evt|msg|que)_[0-9A-HJKMNP-TV-Z]{26}$`)

var (
	ulidMu   sync.Mutex
	lastMs   int64 = -1
	lastRand []byte
)

func encodeTime(ms int64) string {
	out := make([]byte, 10)
	for i := 9; i >= 0; i-- {
		mod := ms % 32
		out[i] = b32[mod]
		ms = (ms - mod) / 32
	}
	return string(out)
}

func randomChars() []byte {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	out := make([]byte, 16)
	for i, b := range buf {
		out[i] = b32[int(b)%32]
	}
	return out
}

func increment(chars []byte) []byte {
	out := make([]byte, len(chars))
	copy(out, chars)
	for i := len(out) - 1; i >= 0; i-- {
		idx := strings.IndexByte(b32, out[i])
		if idx < 31 {
			out[i] = b32[idx+1]
			return out
		}
		out[i] = b32[0]
	}
	return randomChars()
}

// ULID returns a monotonic ULID: two generated in the same millisecond sort in
// creation order, so a directory listing is a timeline.
func ULID() string {
	return ulidAt(time.Now().UnixMilli())
}

func ulidAt(ms int64) string {
	ulidMu.Lock()
	defer ulidMu.Unlock()
	if ms == lastMs && lastRand != nil {
		lastRand = increment(lastRand)
	} else {
		lastRand = randomChars()
	}
	lastMs = ms
	return encodeTime(ms) + string(lastRand)
}

// NewID prefixes a ULID with a record kind, for example clm_ or evt_.
func NewID(prefix string) string { return prefix + "_" + ULID() }

// IsID reports whether a string is a well-formed record identifier.
func IsID(v string) bool { return IDPattern.MatchString(v) }

// NowISO formats a time the way Date#toISOString does: UTC, milliseconds, Z.
func NowISO(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// Now is the current instant in the protocol's string form.
func Now() string { return NowISO(time.Now()) }

// ParseISO reads a timestamp written by either implementation.
func ParseISO(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000Z", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ParseMs is ParseISO in milliseconds, with a false when the value is unusable.
func ParseMs(s string) (int64, bool) {
	t, ok := ParseISO(s)
	if !ok {
		return 0, false
	}
	return t.UnixMilli(), true
}

// CompactTs turns an ISO timestamp into the sortable note filename prefix.
func CompactTs(iso string) string {
	s := strings.NewReplacer("-", "", ":", "").Replace(iso)
	if len(s) > 5 && strings.HasSuffix(s, "Z") && s[len(s)-5] == '.' {
		s = s[:len(s)-5] + s[len(s)-4:]
	}
	return s
}

var durationRe = regexp.MustCompile(`^(\d+)(ms|s|m|h|d)$`)

var durationUnits = map[string]int64{"ms": 1, "s": 1000, "m": 60000, "h": 3600000, "d": 86400000}

// ParseDuration accepts "500ms", "30s", "15m", "2h", "1d". A bare number is
// refused on purpose: "--ttl 90" is exactly the ambiguity that ruins a night.
func ParseDuration(input, flag string) (int64, error) {
	raw := strings.TrimSpace(input)
	m := durationRe.FindStringSubmatch(raw)
	if m == nil {
		hint := "Use 500ms, 30s, 15m, 2h, or 1d."
		if regexp.MustCompile(`^\d+$`).MatchString(raw) {
			hint = fmt.Sprintf("Durations need a unit: %ss, %sm, %sh.", raw, raw, raw)
		}
		return 0, errs.New("E_USAGE", fmt.Sprintf("Invalid %s: '%s'.", flag, raw)).WithHint(hint)
	}
	n, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return 0, errs.New("E_USAGE", fmt.Sprintf("Invalid %s: '%s'.", flag, raw))
	}
	return n * durationUnits[m[2]], nil
}

// HumanMs renders a duration the way the door and the CLI text do.
func HumanMs(ms int64) string {
	if ms < 0 {
		return "expired"
	}
	abs := ms
	if abs < 0 {
		abs = -abs
	}
	s := abs / 1000
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	rs := s % 60
	if m < 60 {
		if rs != 0 {
			return fmt.Sprintf("%dm %ds", m, rs)
		}
		return fmt.Sprintf("%dm", m)
	}
	h := m / 60
	rm := m % 60
	if rm != 0 {
		return fmt.Sprintf("%dh %dm", h, rm)
	}
	return fmt.Sprintf("%dh", h)
}

// SHA256 is the "sha256:<hex>" form the records use.
func SHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ShortHash is the 12-hex-character digest the door and the adapters embed.
func ShortHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:12]
}

// RandomToken mints an ownership token: 24 CSPRNG bytes, base64url, unpadded.
func RandomToken() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

var (
	hostOnce sync.Once
	hostVal  string
)

// HostID is a salted-free digest of the hostname, truncated exactly the way the
// Node implementation truncates it, so both agree on "is this my machine".
func HostID() string {
	hostOnce.Do(func() {
		name, err := os.Hostname()
		if err != nil {
			name = ""
		}
		hostVal = SHA256(name)[:23]
	})
	return hostVal
}

var slugStrip = regexp.MustCompile(`[^a-z0-9-]+`)

// Slug is the filename identity key for an actor: lowercase, ASCII-safe, 24
// characters. Two names that slug the same are the same actor.
func Slug(name string) string { return SlugMax(name, 24) }

// SlugMax is Slug with an explicit maximum length.
func SlugMax(name string, max int) string {
	s := strings.ToLower(name)
	s = slugStrip.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "anon"
	}
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// Jitter spreads retries so two processes never march in lockstep.
func Jitter(ms int64, ratio float64) int64 {
	v := math.Round(float64(ms) * (1 + (mrand.Float64()*2-1)*ratio))
	if v < 1 {
		return 1
	}
	return int64(v)
}

// Sleep is time.Sleep in milliseconds.
func Sleep(ms int64) { time.Sleep(time.Duration(ms) * time.Millisecond) }

// NowMs is the current wall clock in milliseconds, the unit every record uses.
func NowMs() int64 { return time.Now().UnixMilli() }

// Mulberry32 is the seeded PRNG the simulation uses, so a seed reproduces a run.
func Mulberry32(seed uint32) func() float64 {
	a := seed
	return func() float64 {
		a += 0x6d2b79f5
		t := (a ^ (a >> 15)) * (1 | a)
		t = (t + (t^(t>>7))*(61|t)) ^ t
		return float64(t^(t>>14)) / 4294967296.0
	}
}
