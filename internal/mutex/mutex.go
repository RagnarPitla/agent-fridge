// SPDX-License-Identifier: Apache-2.0
// The one pen on the string. mkdir is the mutex. Held for milliseconds, never for a chore.
package mutex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"

	"os/signal"

	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/fsx"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

// Env is the slice of a workspace the mutex needs. Keeping it an interface
// keeps the store package free to import this one.
type Env interface {
	MutexDir() string
	TmpDir() string
	AcquireTimeoutMs() int
	StaleMs() int
	MaxHoldMs() int
	SessionID() string
}

// statLock is a seam. Windows fails a stat of a directory that is pending
// deletion, and the only honest answer to a failed stat is "I cannot tell how
// old this lock is", so tests need a way to produce that failure anywhere.
var statLock = os.Stat

// dropLockOnce makes one attempt to take the lock directory away, and reports
// whether the lock is gone afterwards.
//
// It is a seam for the same reason statLock is. On Windows every operation
// here fails while another process has owner.json open, and waiters open it
// constantly: rename hits a sharing violation, the unlink hits a sharing
// violation, and the rmdir then fails because the directory is not empty.
//
// Renaming aside is tried first because it is atomic: no other process ever
// sees a lock directory whose owner file has gone missing. Taking the lock
// apart in place is the fallback, and does briefly produce that state, which
// is safe but costs any waiter a full stale window.
var dropLockOnce = func(lockDir, ownerFile string) bool {
	dead := lockDir + ".rel-" + util.RandomToken()[:8]
	if err := os.Rename(lockDir, dead); err == nil {
		fsx.RmRF(dead)
		return true
	}
	return !fsx.Exists(lockDir)
}

// dismantleLockInPlace is the last resort when renaming aside keeps failing.
// It is a seam for the same reason: on Windows the sharing violation that
// blocks the rename blocks the unlink and the rmdir too, so a test that only
// models the rename is not modelling Windows.
var dismantleLockInPlace = func(lockDir, ownerFile string) bool {
	fsx.UnlinkQuiet(ownerFile)
	if err := os.Remove(lockDir); err == nil {
		return true
	}
	fsx.RmRF(lockDir)
	return !fsx.Exists(lockDir)
}

// How long a release keeps trying. A release that gives up leaves a lock that
// nobody holds and nobody can take, which stops the whole workspace until the
// stale window expires, so it is worth being patient. In practice the handle
// that blocks it is open for microseconds.
const releaseTimeoutMs = 2000

// Options tune a single acquisition.
type Options struct {
	TimeoutMs int
	StaleMs   int
	OnBreak   func(why string, owner jsonx.Obj)
}

// With runs fn while holding the registry mutex. The lock is a directory, so
// creation is atomic on POSIX, Windows and NFS alike.
func With(ws Env, op string, fn func() error, opts *Options) error {
	lockDir := ws.MutexDir()
	ownerFile := filepath.Join(lockDir, "owner.json")
	limit := ws.AcquireTimeoutMs()
	stale := ws.StaleMs()
	var onBreak func(string, jsonx.Obj)
	if opts != nil {
		if opts.TimeoutMs > 0 {
			limit = opts.TimeoutMs
		}
		if opts.StaleMs > 0 {
			stale = opts.StaleMs
		}
		onBreak = opts.OnBreak
	}
	deadline := util.NowMs() + int64(limit)
	delay := 5
	held := false

	// What the lock looked like the first time we suspected it was dead, and
	// when we first suspected it. A lock whose owner file cannot be read yet
	// is not evidence of a dead process: it is the normal window between
	// mkdir and the owner write. We only break on a suspicion that has held
	// steady for the whole stale window.
	suspectKey := ""
	suspectSince := int64(0)

	// release runs from both the deferred cleanup and the signal goroutine, so
	// it has to be safe to call twice from two goroutines at once.
	var released atomic.Bool
	release := func() {
		if !released.CompareAndSwap(false, true) {
			return
		}
		held = false
		// Keep trying. On Windows any of these operations fails while a
		// waiter has owner.json open, and that failure clears in
		// microseconds, but a single-shot release turns it into a lock
		// nobody holds and nobody can take.
		deadline := util.NowMs() + int64(releaseTimeoutMs)
		for {
			if dropLockOnce(lockDir, ownerFile) {
				return
			}
			if util.NowMs() >= deadline {
				break
			}
			util.Sleep(2)
		}
		// Still stuck after all that. Take it apart in place; if even that
		// fails, the lock is left for stale detection to reclaim, which is
		// slow but never unsafe.
		dismantleLockInPlace(lockDir, ownerFile)
	}

	for !held {
		err := os.Mkdir(lockDir, 0o777)
		if err == nil {
			held = true
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			if errors.Is(err, fs.ErrNotExist) {
				if e2 := fsx.EnsureDir(filepath.Dir(lockDir)); e2 != nil {
					return e2
				}
				continue
			}
			if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS) {
				return errs.New("E_PERMISSION", fmt.Sprintf("Cannot lock %s: %s", lockDir, errCode(err)))
			}
			return err
		}
		owner, ownerOK := fsx.ReadJSONSafe(ownerFile)
		breakIt := false
		why := ""
		key := ""
		if !ownerOK {
			// No readable owner. Do not guess an age from a stat we may not be
			// able to take: on Windows, stat of a directory that is pending
			// deletion fails, and treating that as "modified at the epoch"
			// would break a lock somebody is actively holding.
			key = "unreadable"
			if st, e := statLock(lockDir); e == nil {
				key = fmt.Sprintf("unreadable@%d", st.ModTime().UnixMilli())
				if util.NowMs()-st.ModTime().UnixMilli() > int64(stale) {
					breakIt = true
					why = "unreadable-owner"
				}
			}
			if !breakIt {
				if key != suspectKey {
					suspectKey, suspectSince = key, util.NowMs()
				} else if util.NowMs()-suspectSince > int64(stale) {
					breakIt = true
					why = "unreadable-owner"
				}
			}
		} else {
			acquiredMs, _ := util.ParseMs(owner.Str("acquiredAt"))
			age := util.NowMs() - acquiredMs
			key = ownerKey(owner)
			if owner.Str("host") == util.HostID() && !util.ProcessAlive(owner.Int("pid")) {
				breakIt = true
				why = "owner-process-gone"
			} else if age > int64(stale) {
				breakIt = true
				why = "owner-stale"
			}
			if suspectKey != key {
				suspectKey, suspectSince = key, util.NowMs()
			}
		}
		if breakIt {
			if broke := breakLock(lockDir, ownerFile, key, ownerOK, stale); broke && onBreak != nil {
				if ownerOK {
					onBreak(why, owner)
				} else {
					onBreak(why, nil)
				}
			}
			suspectKey, suspectSince = "", 0
			continue
		}
		if util.NowMs() >= deadline {
			return errs.New("E_MUTEX_TIMEOUT",
				fmt.Sprintf("Another process is holding the registry mutex (%dms).", limit)).
				WithHint("Retry, or run: fridge doctor --fix")
		}
		util.Sleep(util.Jitter(int64(min(delay, 250)), 0.3))
		delay = min(int(float64(delay)*1.6+0.5), 250)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case s := <-sigCh:
			release()
			if s == os.Interrupt {
				os.Exit(130)
			}
			os.Exit(143)
		case <-done:
		}
	}()

	startedAt := util.NowMs()
	defer func() {
		heldMs := util.NowMs() - startedAt
		release()
		signal.Stop(sigCh)
		close(done)
		if heldMs > int64(ws.MaxHoldMs()) {
			fmt.Fprintf(os.Stderr, "warning: held the registry mutex for %dms during '%s'\n", heldMs, op)
		}
	}()

	var sess any
	if ws.SessionID() != "" {
		sess = ws.SessionID()
	}
	if err := fsx.WriteJSONAtomic(ownerFile, jsonx.Obj{
		"acquiredAt": util.Now(),
		"host":       util.HostID(),
		"op":         op,
		"pid":        float64(os.Getpid()),
		"schema":     "wcp/0.1/mutex-owner",
		"sessionId":  sess,
		"writer":     brand.Writer,
	}, ws.TmpDir()); err != nil {
		// The usual reason this fails is that another process judged our lock
		// dead and broke it while we were still setting up. Say so, and do not
		// run fn: whatever we would have done is no longer protected.
		if !fsx.Exists(lockDir) {
			return errs.New("E_MUTEX_TIMEOUT",
				"Another process broke this lock while we were taking it.").
				WithHint("Retry. If it keeps happening, raise mutex.staleMs in .fridge/config.json")
		}
		return err
	}
	return fn()
}

// breakLock removes a lock that a waiter has judged to be dead.
//
// Breaking is the one operation that can violate mutual exclusion, because a
// waiter that deletes a lock somebody is still holding lets a second process
// in. Two things make that safe here.
//
// First, breaking is itself serialised behind a second lock directory, so two
// waiters can never be inside this function at the same time. Without that,
// two waiters can both judge the same corpse, the first deletes it, a third
// process legitimately takes the lock, and the second waiter then deletes a
// live lock that it judged in a previous era.
//
// Second, the evidence is re-read here, under that exclusion, immediately
// before acting. If the lock changed hands since the waiter made up its mind,
// the break is abandoned.
//
// The corpse is claimed by renaming it aside rather than deleting it in place,
// so the removal is a single atomic step from any other process's point of
// view. It returns whether it actually broke anything.
func breakLock(lockDir, ownerFile, key string, ownerOK bool, stale int) bool {
	breakDir := lockDir + ".break"
	if err := os.Mkdir(breakDir, 0o777); err != nil {
		// Somebody else is breaking, or a breaker died holding this. Breaking
		// takes microseconds, so a break lock older than the stale window is
		// unambiguously abandoned. A stat we cannot take proves nothing, so in
		// that case we simply let the caller wait and try again.
		if st, e := statLock(breakDir); e == nil && util.NowMs()-st.ModTime().UnixMilli() > int64(stale) {
			fsx.RmRF(breakDir)
		}
		return false
	}
	defer func() { _ = os.Remove(breakDir) }()

	again, againOK := fsx.ReadJSONSafe(ownerFile)
	if againOK != ownerOK {
		return false
	}
	if againOK && ownerKey(again) != key {
		return false
	}
	if !againOK && !fsx.Exists(lockDir) {
		return false
	}

	dead := lockDir + ".dead-" + util.RandomToken()[:8]
	if err := os.Rename(lockDir, dead); err != nil {
		return false
	}
	fsx.RmRF(dead)
	return true
}

// ownerKey identifies one specific tenancy of the lock, so a waiter can tell
// "the holder I judged" from "whoever holds it now".
func ownerKey(owner jsonx.Obj) string {
	return fmt.Sprintf("%s|%d|%s", owner.Str("host"), owner.Int("pid"), owner.Str("acquiredAt"))
}

func errCode(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "EACCES"
	}
	if errors.Is(err, syscall.EROFS) {
		return "EROFS"
	}
	return "EPERM"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
