// SPDX-License-Identifier: Apache-2.0

package mutex

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/fsx"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

// Windows fails a stat of a directory that is pending deletion. The first
// version of this mutex answered a failed stat with "modified at the epoch",
// which made a lock somebody was actively holding look infinitely stale. A
// waiter would delete the live lock directory, a third process would then
// mkdir successfully, and two processes were inside the critical section at
// once. That is the exact failure this whole project exists to prevent, so it
// is pinned here on every platform rather than only on the one that caught it.
func TestAFailedStatMustNotLookLikeAnAncientLock(t *testing.T) {
	env := newEnv(t)

	original := statLock
	statLock = func(string) (os.FileInfo, error) {
		return nil, errors.New("simulated windows delete-pending stat failure")
	}
	t.Cleanup(func() { statLock = original })

	// A holder that has taken the lock but has not written owner.json yet.
	// This is a real window in every acquisition, not a contrived state.
	if err := os.MkdirAll(env.dir, 0o777); err != nil {
		t.Fatal(err)
	}

	entered := false
	err := With(env, "waiter", func() error {
		entered = true
		return nil
	}, &Options{TimeoutMs: 300, StaleMs: 5000})

	if entered {
		t.Fatal("a waiter broke into a lock it could not prove was dead")
	}
	if err == nil {
		t.Fatal("expected the waiter to give up, got a clean acquisition")
	}
	if code := errCodeOf(err); code != "E_MUTEX_TIMEOUT" {
		t.Fatalf("expected E_MUTEX_TIMEOUT, got %v", err)
	}
	if !fsx.Exists(env.dir) {
		t.Fatal("the waiter deleted a live lock directory")
	}
}

// The other half of the same rule: an unreadable lock that really has sat
// there past the stale window must still be broken, or a crashed process
// wedges the workspace forever.
func TestAnUnreadableLockIsStillBrokenOnceItIsGenuinelyStale(t *testing.T) {
	env := newEnv(t)
	env.staleMs = 60

	original := statLock
	statLock = func(string) (os.FileInfo, error) { return nil, errors.New("simulated stat failure") }
	t.Cleanup(func() { statLock = original })

	if err := os.MkdirAll(env.dir, 0o777); err != nil {
		t.Fatal(err)
	}

	entered := false
	if err := With(env, "waiter", func() error {
		entered = true
		return nil
	}, &Options{TimeoutMs: 4000, StaleMs: 60}); err != nil {
		t.Fatalf("expected the waiter to eventually break in: %v", err)
	}
	if !entered {
		t.Fatal("a genuinely stale lock was never broken")
	}
}

// Breaking is a rename, so a pile-up of waiters cannot delete each other's
// freshly taken locks. Every waiter here is contending on an abandoned lock.
func TestBreakingAStaleLockStillAdmitsOneHolderAtATime(t *testing.T) {
	env := newEnv(t)
	// Long enough that a descheduled goroutine is never mistaken for a dead
	// one, which would make this test measure the Go scheduler instead of the
	// mutex. The corpse below is dated 1999, so it is stale under any window.
	env.staleMs = 5000

	if err := os.MkdirAll(env.dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := fsx.WriteAtomic(filepath.Join(env.dir, "owner.json"),
		`{"acquiredAt":"1999-01-01T00:00:00.000Z","host":"somewhere-else","op":"abandoned","pid":1,"schema":"wcp/0.1/mutex-owner","writer":"test"}`,
		env.tmp); err != nil {
		t.Fatal(err)
	}

	var inside, maxSeen atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := With(env, "storm", func() error {
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
		t.Errorf("saw %d concurrent holders while breaking a stale lock, want 1", maxSeen.Load())
	}
	if fsx.Exists(env.dir) {
		t.Error("lock directory survived the last release")
	}
}

// Releasing moves the lock aside in one step. A waiter must never see a lock
// directory that exists with no owner file inside it because of a release in
// progress, because that state is indistinguishable from a crashed holder.
func TestReleaseLeavesNoHalfDismantledLockBehind(t *testing.T) {
	env := newEnv(t)
	parent := filepath.Dir(env.dir)

	stop := make(chan struct{})
	bad := atomic.Int32{}
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if fsx.Exists(env.dir) && !fsx.Exists(filepath.Join(env.dir, "owner.json")) {
				// Could be the legitimate mkdir-then-write window, so only
				// count it if it is still true a moment later.
				util.Sleep(1)
				if fsx.Exists(env.dir) && !fsx.Exists(filepath.Join(env.dir, "owner.json")) {
					bad.Add(1)
				}
			}
		}
	}()

	for i := 0; i < 40; i++ {
		if err := With(env, "churn", func() error { return nil }, nil); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	util.Sleep(20)

	if fsx.Exists(env.dir) {
		t.Error("lock directory survived the last release")
	}
	leftovers, _ := filepath.Glob(filepath.Join(parent, "registry.lock.d.*"))
	if len(leftovers) != 0 {
		t.Errorf("release left temporary lock corpses behind: %v", leftovers)
	}
}

func errCodeOf(err error) string {
	var app *errs.AppError
	if errors.As(err, &app) {
		return app.Code
	}
	return ""
}
