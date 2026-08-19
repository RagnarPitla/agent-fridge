// SPDX-License-Identifier: Apache-2.0
package commands

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/RagnarPitla/agent-fridge/internal/brand"
	"github.com/RagnarPitla/agent-fridge/internal/errs"
	"github.com/RagnarPitla/agent-fridge/internal/fsx"
	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/output"
	"github.com/RagnarPitla/agent-fridge/internal/render"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

func cmdBoard(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	doc := render.Door(ws)
	if ctx.Flags.Bool("check") {
		onDisk, exists := fsx.ReadTextOr(ws.Paths.Door)
		drift, _, _ := render.Drift(ws, onDisk)
		if drift {
			return 0, errs.New("E_DRIFT", "DOOR.md does not match the state in .fridge/.").
				WithHint(brand.Bin + " render").
				WithDetails(jsonx.Obj{"path": ws.Paths.Door, "exists": exists})
		}
		return ctx.emit("board", output.Result{Data: jsonx.Obj{"drift": false}, Text: "door is up to date"})
	}
	if ctx.Flags.Bool("write") {
		if err := fsx.WriteAtomic(ws.Paths.Door, doc, ws.Paths.Tmp); err != nil {
			return 0, err
		}
	}
	return ctx.emit("board", output.Result{Data: render.Snapshot(ws), Text: doc})
}

func cmdStatus(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	mine := ""
	if ctx.Flags.Bool("mine") {
		mine = ctx.Flags.Str("agent")
		if mine == "" {
			mine = envActor()
		}
	}
	wide := ctx.Flags.Bool("wide")
	if !ctx.Flags.Bool("watch") {
		return ctx.emit("status", output.Result{
			Data: render.Snapshot(ws), Text: render.StatusText(ws, mine, wide),
		})
	}
	intervalMs := 2000
	if v := ctx.Flags.Str("interval"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			intervalMs = int(n)
		}
	}
	if intervalMs < 250 {
		intervalMs = 250
	}
	for {
		live, err := open(ctx)
		if err != nil {
			return 0, err
		}
		fmt.Fprintf(ctx.Out.Stdout, "%s\n\n", render.StatusText(live, mine, wide))
		util.Sleep(int64(intervalMs))
	}
}

func cmdRender(ctx *Ctx) (int, error) {
	ws, err := open(ctx)
	if err != nil {
		return 0, err
	}
	doc := render.Door(ws)
	snap := render.Snapshot(ws)
	targets := []string{}
	if out := ctx.Flags.Str("output"); out != "" {
		targets = append(targets, resolveIn(ws.Root, out))
	} else {
		targets = append(targets, ws.Paths.Door)
	}
	for _, t := range ws.Config.Strings("door.extraTargets") {
		targets = append(targets, resolveIn(ws.Root, t))
	}
	if ctx.Flags.Bool("check") {
		drifted := []string{}
		for _, t := range targets {
			text, _ := fsx.ReadTextOr(t)
			if d, _, _ := render.Drift(ws, text); d {
				drifted = append(drifted, t)
			}
		}
		if len(drifted) > 0 {
			return 0, errs.New("E_DRIFT", fmt.Sprintf("%d generated view(s) are out of date.", len(drifted))).
				WithHint(brand.Bin + " render").
				WithDetails(jsonx.Obj{"report": strings.Join(drifted, "\n"), "targets": strArr(drifted)})
		}
		return ctx.emit("render", output.Result{
			Data: jsonx.Obj{"drift": false, "targets": strArr(targets)},
			Text: "all generated views are up to date",
		})
	}
	for _, t := range targets {
		if err := fsx.WriteAtomic(t, doc, ws.Paths.Tmp); err != nil {
			return 0, err
		}
	}
	statusPath := filepath.Join(ws.Paths.Views, "status.json")
	if err := fsx.WriteJSONAtomic(statusPath, snap, ws.Paths.Tmp); err != nil {
		return 0, err
	}
	lines := []string{}
	for _, t := range append(append([]string{}, targets...), statusPath) {
		rel, err := filepath.Rel(ws.Root, t)
		if err != nil {
			rel = t
		}
		lines = append(lines, "  "+rel)
	}
	return ctx.emit("render", output.Result{
		Data: jsonx.Obj{"targets": strArr(targets), "snapshot": snap},
		Text: fmt.Sprintf("wrote %d generated view(s):\n%s", len(targets)+1, strings.Join(lines, "\n")),
	})
}

func resolveIn(root, p string) string {
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(root, filepath.FromSlash(p)))
}
