// SPDX-License-Identifier: Apache-2.0

package mutex

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/RagnarPitla/agent-fridge/internal/fsx"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

// Windows fails a rename, an unlink and an rmdir of a lock directory while
// another process has owner.json open, and waiters open it constantly to read
// who is holding the lock. The failure clears in microseconds.
//
// A release that tried once and gave up turned that microsecond into a
// permanent outage: the lock directory survived a release nobody was holding,
// every later acquirer sat there until its timeout, and the workspace was
// stuck until the stale window expired. It cost one goroutine in sixteen to
// lose the whole run.
//
// So a release retries. These tests model the sharing violation on every
// platform rather than only on the one that caught it.
func failDropsFor(t *testing.T, ms int64) *atomic.Int32 {
	t.Helper()
	originalDrop, originalDismantle := dropLockOnce, dismantleLockInPlace
	until := util.NowMs() + ms
	var attempts atomic.Int32
	// A sharing violation blocks every way of removing the lock at once, so
	// both paths are blocked together.
	dropLockOnce = func(lockDir, ownerFile string) bool {
		attempts.Add(1)
		if util.NowMs() < until {
			return false
		}
		return originalDrop(lockDir, ownerFile)
	}
	dismantleLockInPlace = func(lockDir, ownerFile string) bool {
		if util.NowMs() < until {
			return false
		}
		return originalDismantle(lockDir, ownerFile)
	}
	t.Cleanup(func() { dropLockOnce, dismantleLockInPlace = originalDrop, originalDismantle })
	return &attempts
}

func TestReleaseKeepsTryingWhenTheFilesystemSaysNo(t *testing.T) {
	env := newEnv(t)
	attempts := failDropsFor(t, 200)

	if err := With(env, "holder", func() error { return nil }, nil); err != nil {
		t.Fatalf("With returned %v", err)
	}
	if fsx.Exists(env.dir) {
		t.Fatal("the lock survived a release that was only transiently blocked")
	}
	if n := attempts.Load(); n < 2 {
		t.Fatalf("release gave up after %d attempt(s); it must retry", n)
	}
}

// The point of retrying is that the next acquirer does not have to wait for
// the stale window. This is the shape of the CI failure: one blocked release,
// then everybody else timing out.
func TestABlockedReleaseDoesNotStallTheNextAcquirer(t *testing.T) {
	env := newEnv(t)
	failDropsFor(t, 200)

	if err := With(env, "holder", func() error { return nil }, nil); err != nil {
		t.Fatalf("first holder: %v", err)
	}
	// StaleMs is far beyond the test's patience on purpose: if this passes by
	// breaking a stale lock rather than by a clean release, it fails here.
	err := With(env, "next", func() error { return nil }, &Options{TimeoutMs: 30000, StaleMs: 600000})
	if err != nil {
		t.Fatalf("the next acquirer could not get in after a transiently blocked release: %v", err)
	}
}

// Under contention it only takes one blocked release to strand everybody.
func TestOneBlockedReleaseDoesNotStrandSixteenWaiters(t *testing.T) {
	env := newEnv(t)

	originalDrop, originalDismantle := dropLockOnce, dismantleLockInPlace
	var blockedUntil atomic.Int64
	blocked := func() bool {
		// Block every removal for 150ms starting at the first release, the way
		// one sharing violation would.
		blockedUntil.CompareAndSwap(0, util.NowMs()+150)
		return util.NowMs() < blockedUntil.Load()
	}
	dropLockOnce = func(lockDir, ownerFile string) bool {
		if blocked() {
			return false
		}
		return originalDrop(lockDir, ownerFile)
	}
	dismantleLockInPlace = func(lockDir, ownerFile string) bool {
		if blocked() {
			return false
		}
		return originalDismantle(lockDir, ownerFile)
	}
	t.Cleanup(func() { dropLockOnce, dismantleLockInPlace = originalDrop, originalDismantle })

	var wg sync.WaitGroup
	var failures atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := With(env, "worker", func() error { util.Sleep(2); return nil }, &Options{TimeoutMs: 30000, StaleMs: 600000}); err != nil {
				failures.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := failures.Load(); n != 0 {
		t.Fatalf("%d of 16 workers could not get the lock after one blocked release", n)
	}
	if fsx.Exists(env.dir) {
		t.Fatal("lock directory survived the last release")
	}
}

// If the block never clears, the release must not leave a lock that looks
// alive forever. Giving up is allowed; giving up quietly and leaving a lock
// that stale detection cannot reclaim is not.
func TestAReleaseThatCannotSucceedLeavesAReclaimableLock(t *testing.T) {
	env := newEnv(t)

	originalDrop, originalDismantle := dropLockOnce, dismantleLockInPlace
	dropLockOnce = func(string, string) bool { return false }
	dismantleLockInPlace = func(string, string) bool { return false }
	t.Cleanup(func() { dropLockOnce, dismantleLockInPlace = originalDrop, originalDismantle })

	if err := With(env, "holder", func() error { return nil }, &Options{TimeoutMs: 1000, StaleMs: 600000}); err != nil {
		t.Fatalf("With returned %v", err)
	}
	dropLockOnce, dismantleLockInPlace = originalDrop, originalDismantle

	// The lock is now a corpse. A later acquirer with a short stale window
	// must be able to reclaim it rather than wait forever.
	util.Sleep(120)
	entered := false
	err := With(env, "next", func() error { entered = true; return nil }, &Options{TimeoutMs: 30000, StaleMs: 100})
	if err != nil || !entered {
		t.Fatalf("an abandoned lock was not reclaimable: err=%v entered=%v", err, entered)
	}
}
