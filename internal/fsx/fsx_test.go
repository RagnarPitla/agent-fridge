// SPDX-License-Identifier: Apache-2.0
package fsx

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
)

func scratch(t *testing.T, label string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod")
		}
		dir = parent
	}
	root := filepath.Join(dir, ".scratch", "gotest", label+"-"+strings.ReplaceAll(t.Name(), "/", "_"))
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return root
}

// A reader must never observe a half-written file. WriteAtomic writes to a
// unique temp file and renames, so the target either has the old bytes or the
// new bytes and nothing in between.
func TestWriteAtomicLeavesNoPartialFile(t *testing.T) {
	root := scratch(t, "fsx")
	tmp := filepath.Join(root, "tmp")
	target := filepath.Join(root, "nested", "deep", "file.txt")

	if err := WriteAtomic(target, "first\n", tmp); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadTextOr(target)
	if got != "first\n" {
		t.Fatalf("got %q", got)
	}
	if err := WriteAtomic(target, "second\n", tmp); err != nil {
		t.Fatal(err)
	}
	got, _ = ReadTextOr(target)
	if got != "second\n" {
		t.Fatalf("overwrite produced %q", got)
	}
	entries, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("temp directory should be empty after a successful write, has %d entries", len(entries))
	}
}

func TestConcurrentWritersNeverInterleave(t *testing.T) {
	root := scratch(t, "fsx")
	tmp := filepath.Join(root, "tmp")
	target := filepath.Join(root, "hot.txt")
	bodies := []string{strings.Repeat("a", 4096) + "\n", strings.Repeat("b", 4096) + "\n"}

	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := WriteAtomic(target, bodies[i%2], tmp); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, _ := ReadTextOr(target)
	if got != bodies[0] && got != bodies[1] {
		t.Errorf("observed a torn write of %d bytes", len(got))
	}
}

func TestCreateExclusiveRefusesToOverwrite(t *testing.T) {
	root := scratch(t, "fsx")
	target := filepath.Join(root, "once.txt")
	if err := CreateExclusive(target, "one\n", ""); err != nil {
		t.Fatal(err)
	}
	if err := CreateExclusive(target, "two\n", ""); err == nil {
		t.Fatalf("second create must fail")
	}
	got, _ := ReadTextOr(target)
	if got != "one\n" {
		t.Errorf("file was overwritten: %q", got)
	}
}

func TestCreateExclusivePublishesOnlyCompleteContent(t *testing.T) {
	root := scratch(t, "fsx")
	tmpDir := filepath.Join(root, "tmp")
	finalPath := filepath.Join(root, "notes", "atomic.json")
	complete := `{"schema":"wcp/0.1/note","summary":"complete before publish"}`
	observed := false
	previous := beforeExclusivePublish
	beforeExclusivePublish = func(tmp, final string) {
		observed = true
		if final != finalPath {
			t.Fatalf("published %s, want %s", final, finalPath)
		}
		if _, err := os.Stat(final); !os.IsNotExist(err) {
			t.Fatalf("final path existed before publication: %v", err)
		}
		raw, err := os.ReadFile(tmp)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != complete {
			t.Fatalf("staged content = %q, want %q", raw, complete)
		}
	}
	t.Cleanup(func() { beforeExclusivePublish = previous })

	if err := CreateExclusive(finalPath, complete, tmpDir); err != nil {
		t.Fatal(err)
	}
	if !observed {
		t.Fatal("pre-publication seam was not reached")
	}
	raw, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != complete {
		t.Fatalf("published content = %q, want %q", raw, complete)
	}
}

func TestReadJSONSafeReportsCorruption(t *testing.T) {
	root := scratch(t, "fsx")
	good := filepath.Join(root, "good.json")
	bad := filepath.Join(root, "bad.json")
	if err := WriteAtomic(good, "{\"a\":1}\n", filepath.Join(root, "tmp")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAtomic(bad, "{not json", filepath.Join(root, "tmp")); err != nil {
		t.Fatal(err)
	}
	if v, ok := ReadJSONSafe(good); !ok || v.Num("a") != 1 {
		t.Errorf("good file did not parse")
	}
	if _, ok := ReadJSONSafe(bad); ok {
		t.Errorf("corrupt file reported as readable")
	}
	if _, ok := ReadJSONSafe(filepath.Join(root, "missing.json")); ok {
		t.Errorf("missing file reported as readable")
	}
}

func TestListJSONAndWalkJSONAreSortedAndFiltered(t *testing.T) {
	root := scratch(t, "fsx")
	tmp := filepath.Join(root, "tmp")
	for _, rel := range []string{"b.json", "a.json", "notes/2.json", "notes/1.json", "skip.txt"} {
		if err := WriteAtomic(filepath.Join(root, filepath.FromSlash(rel)), "{}\n", tmp); err != nil {
			t.Fatal(err)
		}
	}
	top := ListJSON(root)
	if len(top) != 2 || filepath.Base(top[0]) != "a.json" || filepath.Base(top[1]) != "b.json" {
		t.Errorf("ListJSON returned %v", top)
	}
	all := WalkJSON(root)
	if len(all) != 4 {
		t.Errorf("WalkJSON returned %d files, want 4: %v", len(all), all)
	}
	for _, f := range all {
		if filepath.Ext(f) != ".json" {
			t.Errorf("WalkJSON returned a non-JSON file %s", f)
		}
	}
}

func TestWriteJSONAtomicIsStable(t *testing.T) {
	root := scratch(t, "fsx")
	target := filepath.Join(root, "rec.json")
	rec := jsonx.Obj{"z": jsonx.Obj{"m": true}, "a": float64(1)}
	if err := WriteJSONAtomic(target, rec, filepath.Join(root, "tmp")); err != nil {
		t.Fatal(err)
	}
	got, _ := ReadTextOr(target)
	want := "{\n  \"a\": 1,\n  \"z\": {\n    \"m\": true\n  }\n}\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
