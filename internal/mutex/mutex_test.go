// SPDX-License-Identifier: Apache-2.0
package mutex

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/fsx"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

type fakeEnv struct {
	dir       string
	tmp       string
	timeoutMs int
	staleMs   int
	maxHoldMs int
	session   string
}

func (f *fakeEnv) MutexDir() string      { return f.dir }
func (f *fakeEnv) TmpDir() string        { return f.tmp }
func (f *fakeEnv) AcquireTimeoutMs() int { return f.timeoutMs }
func (f *fakeEnv) StaleMs() int          { return f.staleMs }
func (f *fakeEnv) MaxHoldMs() int        { return f.maxHoldMs }
func (f *fakeEnv) SessionID() string     { return f.session }

func newEnv(t *testing.T) *fakeEnv {
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
	root := filepath.Join(dir, ".scratch", "gotest", "mutex-"+strings.ReplaceAll(t.Name(), "/", "_"))
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return &fakeEnv{
		dir: filepath.Join(root, "registry.lock.d"), tmp: filepath.Join(root, "tmp"),
		timeoutMs: 4000, staleMs: 30000, maxHoldMs: 5000, session: "ses_test",
	}
}

// The critical section must be genuinely exclusive: no two goroutines may be
// inside fn at the same instant. Counting entries, not durations.
func TestOnlyOneHolderAtATime(t *testing.T) {
	env := newEnv(t)
	var inside atomic.Int32
	var maxSeen atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := With(env, "test", func() error {
				n := inside.Add(1)
				for {
					m := maxSeen.Load()
					if n <= m || maxSeen.CompareAndSwap(m, n) {
						break
					}
				}
				util.Sleep(2)
				inside.Add(-1)
				return nil
			}, nil)
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if maxSeen.Load() != 1 {
		t.Errorf("saw %d concurrent holders, want 1", maxSeen.Load())
	}
	if fsx.Exists(env.dir) {
		t.Errorf("lock directory survived the last release")
	}
}

func TestLockIsReleasedWhenTheBodyFails(t *testing.T) {
	env := newEnv(t)
	boom := errs.New("E_INTERNAL", "boom")
	if err := With(env, "test", func() error { return boom }, nil); err != boom {
		t.Fatalf("With swallowed the body error: %v", err)
	}
	if fsx.Exists(env.dir) {
		t.Fatalf("lock leaked after a failing body")
	}
	if err := With(env, "test", func() error { return nil }, nil); err != nil {
		t.Fatalf("lock was not reusable: %v", err)
	}
}

func TestTimeoutReportsMutexTimeoutWithTheOwner(t *testing.T) {
	env := newEnv(t)
	env.timeoutMs = 150
	if err := os.MkdirAll(env.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := jsonx.Obj{
		"acquiredAt": util.Now(), "host": util.HostID(), "op": "squatter",
		"pid": float64(os.Getpid()), "sessionId": "ses_other",
	}
	if err := fsx.WriteJSONAtomic(filepath.Join(env.dir, "owner.json"), owner, env.tmp); err != nil {
		t.Fatal(err)
	}
	err := With(env, "test", func() error {
		t.Errorf("body must not run while the lock is held")
		return nil
	}, nil)
	app := errs.As(err)
	if app == nil || app.Code != "E_MUTEX_TIMEOUT" || app.ExitCode != 20 {
		t.Fatalf("got %v, want E_MUTEX_TIMEOUT (20)", err)
	}
	// Parity with src/core/mutex.mjs: the message carries the budget and the
	// hint points at doctor. No owner details are attached on either side.
	if !strings.Contains(app.Msg, "150ms") || !strings.Contains(app.Hint, "doctor") {
		t.Errorf("timeout error was %q / %q", app.Msg, app.Hint)
	}
}

// A crashed holder must not wedge the workspace forever. An owner file older
// than staleMs is broken and the waiter proceeds.
func TestStaleLockIsBrokenAndReported(t *testing.T) {
	env := newEnv(t)
	env.timeoutMs = 2000
	if err := os.MkdirAll(env.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	owner := jsonx.Obj{
		"acquiredAt": util.NowISO(time.Now().Add(-60 * time.Second)), "host": util.HostID(), "op": "ghost",
		"pid": float64(os.Getpid()), "sessionId": "ses_other",
	}
	if err := fsx.WriteJSONAtomic(filepath.Join(env.dir, "owner.json"), owner, env.tmp); err != nil {
		t.Fatal(err)
	}
	broke := ""
	ran := false
	err := With(env, "test", func() error { ran = true; return nil }, &Options{
		StaleMs: 1000,
		OnBreak: func(why string, _ jsonx.Obj) { broke = why },
	})
	if err != nil {
		t.Fatalf("stale lock was not broken: %v", err)
	}
	if !ran {
		t.Errorf("body never ran")
	}
	if broke == "" {
		t.Errorf("breaking a stale lock must be reported, not silent")
	}
}

func TestOwnerFileNamesTheHolder(t *testing.T) {
	env := newEnv(t)
	var seen jsonx.Obj
	if err := With(env, "claim", func() error {
		v, ok := fsx.ReadJSONSafe(filepath.Join(env.dir, "owner.json"))
		if !ok {
			t.Fatalf("owner.json missing while the lock is held")
		}
		seen = v
		return nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	if seen.Str("op") != "claim" {
		t.Errorf("op = %q", seen.Str("op"))
	}
	if seen.Int("pid") != os.Getpid() {
		t.Errorf("pid = %d want %d", seen.Int("pid"), os.Getpid())
	}
	if seen.Str("sessionId") != "ses_test" || seen.Str("host") != util.HostID() {
		t.Errorf("owner record is incomplete: %v", seen)
	}
}
