// SPDX-License-Identifier: Apache-2.0
// One simulation housemate. Runs as its own OS process so the contention is real,
// not cooperative scheduling inside a single goroutine.
package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
	"github.com/RagnarPitla/agent-fridge/internal/util"
)

// simWorkerCommand is deliberately absent from the command table: it is an
// implementation detail of `fridge simulate`, not part of the public surface.
const simWorkerCommand = "__sim-worker"

var simPool = []string{"sim/alpha/**", "sim/beta/**", "sim/gamma/**", "sim/alpha/deep/**", "sim/delta/notes.md"}

var simVendors = []string{"claude", "copilot", "codex", "human", "other"}

func envInt(name string, fallback int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func simWorker(stdout io.Writer) int {
	index := envInt("FRIDGE_SIM_INDEX", 0)
	seed := envInt("FRIDGE_SIM_SEED", 1)
	durationMs := envInt("FRIDGE_SIM_DURATION", 5000)
	name := fmt.Sprintf("sim-%02d", index)
	os.Setenv("FRIDGE_ACTOR", name)

	rand := util.Mulberry32(uint32(seed))
	pick := func(arr []string) string { return arr[int(rand()*float64(len(arr)))] }

	claims, conflicts, pins, releases, abandoned, mutexTimeouts := 0, 0, 0, 0, 0, 0
	errList := jsonx.Arr{}
	quietly := func(args ...string) int { return Main(args, io.Discard, io.Discard) }

	quietly("join", "--agent", name, "--vendor", simVendors[index%len(simVendors)], "--quiet")

	end := util.NowMs() + int64(durationMs)
	for util.NowMs() < end {
		scope := pick(simPool)
		code := quietly("claim", scope, "--task", fmt.Sprintf("sim work %d", claims), "--ttl", "4s", "--quiet")
		if code == 10 {
			conflicts++
			util.Sleep(int64(20 + int(rand()*60)))
			continue
		}
		if code == 20 {
			mutexTimeouts++
			util.Sleep(50)
			continue
		}
		if code != 0 {
			errList = append(errList, fmt.Sprintf("claim %s -> %d", scope, code))
			util.Sleep(30)
			continue
		}
		claims++

		noteCount := 1 + int(rand()*3)
		for i := 0; i < noteCount; i++ {
			c := quietly("pin", fmt.Sprintf("%s touched %s step %d", name, scope, i), "--quiet")
			if c == 0 {
				pins++
			} else {
				errList = append(errList, fmt.Sprintf("pin -> %d", c))
			}
		}
		if rand() < 0.4 {
			quietly("heartbeat", "--quiet")
		}
		util.Sleep(int64(10 + int(rand()*80)))

		// One in eight rounds we walk away without tidying up, exactly like a
		// crashed terminal. The lease must expire and somebody else must be
		// able to take over.
		if rand() < 0.125 {
			abandoned++
			util.Sleep(30)
			continue
		}
		rc := quietly("release", "--all", "--outcome", "done", "--note", fmt.Sprintf("sim round %d", claims), "--quiet")
		if rc == 0 {
			releases++
		} else {
			errList = append(errList, fmt.Sprintf("release -> %d", rc))
		}
	}

	quietly("release", "--all", "--outcome", "abandoned", "--note", "simulation over", "--quiet")
	stats := jsonx.Obj{
		"name": name, "claims": float64(claims), "conflicts": float64(conflicts),
		"pins": float64(pins), "releases": float64(releases), "abandoned": float64(abandoned),
		"mutexTimeouts": float64(mutexTimeouts), "errors": errList,
	}
	fmt.Fprintf(stdout, "%s\n", jsonx.Compact(stats))
	if len(errList) > 0 {
		return 1
	}
	return 0
}

func runSimWorker(self, root string, index, seed, durationMs int) simResult {
	cmd := exec.Command(self, simWorkerCommand)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"FRIDGE_SIM_INDEX="+strconv.Itoa(index),
		"FRIDGE_SIM_SEED="+strconv.Itoa(seed),
		"FRIDGE_SIM_DURATION="+strconv.Itoa(durationMs),
		"FRIDGE_SIM_ROOT="+root)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			code = 1
			errBuf.WriteString(err.Error())
		}
	}
	var stats jsonx.Obj
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if v, err := jsonx.ParseObj([]byte(lines[len(lines)-1])); err == nil {
		stats = v
	}
	stderr := errBuf.String()
	if len(stderr) > 2000 {
		stderr = stderr[len(stderr)-2000:]
	}
	return simResult{Index: index, Code: code, Stats: stats, Stderr: stderr}
}
