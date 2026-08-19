// SPDX-License-Identifier: Apache-2.0
// The one pen on the string. mkdir is the mutex. Held for milliseconds, never for a chore.
package mutex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

	release := func() {
		if !held {
			return
		}
		held = false
		fsx.UnlinkQuiet(ownerFile)
		if err := os.Remove(lockDir); err != nil {
			fsx.RmRF(lockDir)
		}
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
		if !ownerOK {
			var mtime int64
			if st, e := os.Stat(lockDir); e == nil {
				mtime = st.ModTime().UnixMilli()
			}
			if util.NowMs()-mtime > int64(stale) {
				breakIt = true
				why = "unreadable-owner"
			}
		} else {
			acquiredMs, _ := util.ParseMs(owner.Str("acquiredAt"))
			age := util.NowMs() - acquiredMs
			if owner.Str("host") == util.HostID() && !util.ProcessAlive(owner.Int("pid")) {
				breakIt = true
				why = "owner-process-gone"
			} else if age > int64(stale) {
				breakIt = true
				why = "owner-stale"
			}
		}
		if breakIt {
			if onBreak != nil {
				if ownerOK {
					onBreak(why, owner)
				} else {
					onBreak(why, nil)
				}
			}
			fsx.RmRF(lockDir)
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
		return err
	}
	return fn()
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
