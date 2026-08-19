// SPDX-License-Identifier: Apache-2.0
package errs

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
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
			t.Fatal("could not locate go.mod")
		}
		dir = parent
	}
}

// The exit code table is the public API of the CLI. spec/exit-codes.md is
// generated from the Node table, so parsing it here proves both implementations
// agree number for number.
func TestExitTableMatchesSpec(t *testing.T) {
	f, err := os.Open(filepath.Join(repoRoot(t), "spec", "exit-codes.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	spec := map[string]int{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cols := strings.Split(strings.Trim(line, "|"), "|")
		if len(cols) < 3 {
			continue
		}
		num := strings.Trim(strings.TrimSpace(cols[0]), "`")
		code := strings.Trim(strings.TrimSpace(cols[1]), "`")
		n, err := strconv.Atoi(num)
		if err != nil {
			continue
		}
		spec[code] = n
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(spec) < 20 {
		t.Fatalf("spec/exit-codes.md parsed only %d rows", len(spec))
	}
	for code, want := range spec {
		got, ok := Exit[code]
		if !ok {
			t.Errorf("%s is in spec/exit-codes.md but not in the Go table", code)
			continue
		}
		if got != want {
			t.Errorf("%s: Go says %d, spec says %d", code, got, want)
		}
	}
	for code := range Exit {
		if _, ok := spec[code]; !ok {
			t.Errorf("%s is in the Go table but not in spec/exit-codes.md", code)
		}
	}
}

func TestEveryCodeIsDocumentedAndBelow126(t *testing.T) {
	seen := map[int]string{}
	for code, n := range Exit {
		if ExitDoc[code] == "" {
			t.Errorf("%s has no documentation string", code)
		}
		if n >= 126 {
			t.Errorf("%s is %d, which collides with the shell's 127/128+N range", code, n)
		}
		if prev, ok := seen[n]; ok {
			t.Errorf("exit %d is used by both %s and %s", n, prev, code)
		}
		seen[n] = code
	}
	if Exit["OK"] != 0 {
		t.Errorf("OK must be 0, got %d", Exit["OK"])
	}
}

func TestNewCarriesTheExitCode(t *testing.T) {
	err := New("E_CONFLICT", "taken").WithHint("wait").WithDetails(nil)
	if err.ExitCode != 10 {
		t.Errorf("exit code %d want 10", err.ExitCode)
	}
	if err.Code != "E_CONFLICT" || err.Msg != "taken" || err.Hint != "wait" {
		t.Errorf("unexpected error payload %+v", err)
	}
	if got := As(error(err)); got == nil || got.Code != "E_CONFLICT" {
		t.Errorf("As did not recover the AppError")
	}
	if As(os.ErrNotExist) != nil {
		t.Errorf("As must return nil for a foreign error")
	}
}

func TestUnknownCodePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("New with an unknown code must panic rather than invent an exit code")
		}
	}()
	New("E_NOT_A_REAL_CODE", "nope")
}

func TestInternalWrapsAnyError(t *testing.T) {
	err := Internal(os.ErrPermission)
	if err.Code != "E_INTERNAL" || err.ExitCode != 1 {
		t.Errorf("Internal produced %+v", err)
	}
}
