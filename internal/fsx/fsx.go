// SPDX-License-Identifier: Apache-2.0
// Atomic filesystem primitives. mkdir, open(O_EXCL), rename. Nothing else.
// Ported from src/core/fsx.mjs: every mutable record is staged in tmp/, fsynced
// and renamed, so a reader sees the whole old version or the whole new one.
package fsx

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

var beforeExclusivePublish = func(_, _ string) {}

const isWindows = runtime.GOOS == "windows"

// EnsureDir creates a directory tree, mapping a read-only or forbidden target
// onto E_PERMISSION rather than a raw errno.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return errs.New("E_PERMISSION", "Cannot create "+dir+": "+err.Error())
		}
		return err
	}
	return nil
}

func fsyncDir(dir string) {
	if isWindows {
		return
	}
	f, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = f.Sync()
	_ = f.Close()
}

// WriteAtomic stages bytes in tmpDir, fsyncs them, and renames over the target.
// The rename is retried on Windows, where an antivirus scanner or the indexer
// can hold a transient handle and turn a legal replace into EPERM.
func WriteAtomic(finalPath, text, tmpDir string) error {
	dir := filepath.Dir(finalPath)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	if err := EnsureDir(tmpDir); err != nil {
		return err
	}
	tmp := filepath.Join(tmpDir, strconv.Itoa(os.Getpid())+"-"+util.ULID()+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return errs.New("E_PERMISSION", "Cannot write "+finalPath+": "+err.Error())
		}
		return errs.New("E_STATE_CORRUPT", "Failed staging "+finalPath+": "+err.Error())
	}
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return errs.New("E_STATE_CORRUPT", "Failed staging "+finalPath+": "+err.Error())
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return errs.New("E_STATE_CORRUPT", "Failed staging "+finalPath+": "+err.Error())
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return errs.New("E_STATE_CORRUPT", "Failed staging "+finalPath+": "+err.Error())
	}
	renamed := false
	for attempt := 1; attempt <= 6 && !renamed; attempt++ {
		err = os.Rename(tmp, finalPath)
		if err == nil {
			renamed = true
			break
		}
		if isWindows && attempt < 6 {
			time.Sleep(time.Duration(10*(1<<(attempt-1))) * time.Millisecond)
			continue
		}
		_ = os.Remove(tmp)
		return errs.New("E_STATE_CORRUPT", "Failed replacing "+finalPath+": "+err.Error())
	}
	fsyncDir(dir)
	return nil
}

// CreateExclusive is the write-once path used for notes: O_EXCL means two
// processes can never end up writing the same file.
func CreateExclusive(finalPath, text string, tmpDir string) error {
	dir := filepath.Dir(finalPath)
	if err := EnsureDir(dir); err != nil {
		return err
	}
	staging := tmpDir
	if staging == "" {
		staging = filepath.Join(dir, ".tmp")
	}
	if err := EnsureDir(staging); err != nil {
		return err
	}
	tmp := filepath.Join(staging, fmt.Sprintf("%d-%s.tmp", os.Getpid(), util.ULID()))
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return errs.New("E_STATE_CORRUPT", "Failed staging "+finalPath+": "+err.Error())
	}
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		UnlinkQuiet(tmp)
		return errs.New("E_STATE_CORRUPT", "Failed staging "+finalPath+": "+err.Error())
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		UnlinkQuiet(tmp)
		return errs.New("E_STATE_CORRUPT", "Failed staging "+finalPath+": "+err.Error())
	}
	if err := f.Close(); err != nil {
		UnlinkQuiet(tmp)
		return errs.New("E_STATE_CORRUPT", "Failed staging "+finalPath+": "+err.Error())
	}
	beforeExclusivePublish(tmp, finalPath)
	// Link, not open+write. A reader that opens the final name always sees the
	// whole note, because the name only exists once the bytes are on disk.
	if err := os.Link(tmp, finalPath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			UnlinkQuiet(tmp)
			return err
		}
		// Filesystems without hard links (some SMB shares, FAT). Rename is
		// still atomic in content, and the caller's names carry a random id,
		// so losing link's exclusivity here costs nothing that matters.
		if Exists(finalPath) {
			UnlinkQuiet(tmp)
			return fs.ErrExist
		}
		if err := os.Rename(tmp, finalPath); err != nil {
			UnlinkQuiet(tmp)
			return err
		}
		return nil
	}
	UnlinkQuiet(tmp)
	return nil
}

// WriteJSONAtomic writes a record in the protocol's canonical JSON form.
func WriteJSONAtomic(path string, obj jsonx.Obj, tmpDir string) error {
	return WriteAtomic(path, jsonx.Stable(obj), tmpDir)
}

// CreateJSONExclusive writes a write-once record in canonical JSON form.
func CreateJSONExclusive(path string, obj jsonx.Obj, tmpDir string) error {
	return CreateExclusive(path, jsonx.Stable(obj), tmpDir)
}

// ReadJSON reads a record, reporting unparseable bytes as E_STATE_CORRUPT
// rather than crashing or silently skipping.
func ReadJSON(file string) (jsonx.Obj, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	obj, err := jsonx.ParseObj(raw)
	if err != nil {
		return nil, errs.New("E_STATE_CORRUPT", "Unparseable record: "+file).WithHint("fridge doctor --fix")
	}
	return obj, nil
}

// ReadJSONSafe never fails: a damaged record becomes a finding, not a crash.
func ReadJSONSafe(file string) (jsonx.Obj, bool) {
	obj, err := ReadJSON(file)
	if err != nil {
		return nil, false
	}
	return obj, true
}

// ListJSON returns the .json files directly inside dir, sorted by filename, in
// the same order Array#sort gives the Node implementation.
func ListJSON(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{}
	}
	names := []string{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Slice(names, func(i, j int) bool { return jsonx.LessUTF16(names[i], names[j]) })
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, filepath.Join(dir, n))
	}
	return out
}

// WalkJSON returns every .json file under dir, depth first, name sorted.
func WalkJSON(dir string) []string {
	out := []string{}
	walkJSON(dir, &out)
	return out
}

func walkJSON(dir string, out *[]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	isDir := map[string]bool{}
	for _, e := range entries {
		names = append(names, e.Name())
		isDir[e.Name()] = e.IsDir()
	}
	sort.Slice(names, func(i, j int) bool { return jsonx.LessUTF16(names[i], names[j]) })
	for _, n := range names {
		full := filepath.Join(dir, n)
		if isDir[n] {
			walkJSON(full, out)
		} else if strings.HasSuffix(n, ".json") {
			*out = append(*out, full)
		}
	}
}

// Exists reports whether a path is there at all.
func Exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// IsDir reports whether a path is a directory, following symlinks.
func IsDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// RmRF removes a tree and does not complain when it was already gone.
func RmRF(p string) { _ = os.RemoveAll(p) }

// UnlinkQuiet removes a file and treats a missing file as success.
func UnlinkQuiet(p string) { _ = os.Remove(p) }

// ReadTextOr reads a file, returning ok=false when it cannot be read at all.
func ReadTextOr(p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(b), true
}
