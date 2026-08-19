// SPDX-License-Identifier: Apache-2.0
// Notes are durable, and on most workspaces they are committed to Git. Every
// piece of free text that reaches a note is checked here, not just the body of
// `fridge pin`: a task description and a release note land in exactly the same
// permanent file.
package secrets

import (
	"regexp"
	"sort"

	"github.com/RagnarPitla/agent-fridge/internal/errs"
)

type pattern struct {
	re   *regexp.Regexp
	what string
}

var table = []pattern{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`), "a private key"},
	{regexp.MustCompile(`\bghp_[A-Za-z0-9]{20,}\b`), "a GitHub token"},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`), "a GitHub fine-grained token"},
	{regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`), "an AWS access key id"},
	{regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`), "an OpenAI-style key"},
	{regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`), "a Slack token"},
	{regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|client[_-]?secret)\s*[=:]\s*\S{8,}`), "a credential assignment"},
}

// Looks names the first credential shape it recognises, or "".
func Looks(text string) string {
	if text == "" {
		return ""
	}
	for _, p := range table {
		if p.re.MatchString(text) {
			return p.what
		}
	}
	return ""
}

// Guard refuses to record any of these fields if one of them looks like a
// credential. fields maps a flag name to its value, so the error can say which.
func Guard(fields map[string]string, allow bool) error {
	if allow {
		return nil
	}
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, where := range names {
		if found := Looks(fields[where]); found != "" {
			return errs.New("E_USAGE", where+" looks like it contains "+found+".").
				WithHint("That text becomes a durable note. Remove it, or pass --allow-secret-like if it is a false positive.").
				WithDetails(map[string]any{"field": where, "kind": found})
		}
	}
	return nil
}
