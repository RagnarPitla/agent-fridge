// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
)

// The overview is derived, which is only a useful promise if it is derived
// eagerly. If a command writes a note and forgets to re-render, the door
// silently lags behind the truth and `doctor --check` reports drift that no
// human caused. This walks a workspace through every mutating command and
// asserts the derived view is consistent after each one.
func TestDerivedViewNeverLagsBehindAMutation(t *testing.T) {
	root := makeRepo(t, "derived")
	if code := fridge(t, root, []string{"init", "--no-adapters", "--quiet"}).Code; code != 0 {
		t.Fatalf("init exited %d", code)
	}
	if code := fridge(t, root, []string{"doctor", "--check"}).Code; code != 0 {
		t.Fatalf("a freshly initialised fridge should already be tidy, got %d", code)
	}

	step := func(label string, args []string, expected int) runResult {
		t.Helper()
		r := fridge(t, root, args)
		if r.Code != expected {
			t.Fatalf("%s: expected exit %d, got %d\n%s", label, expected, r.Code, r.Stderr)
		}
		check := fridge(t, root, []string{"doctor", "--check"})
		if check.Code != 0 {
			t.Fatalf("%s left the door out of date (doctor exit %d).\n"+
				"Whatever %s writes, it must re-render the derived view.\n%s%s",
				label, check.Code, label, check.Stdout, check.Stderr)
		}
		return r
	}

	step("join alice", []string{"join", "--agent", "alice", "--vendor", "human"}, 0)
	step("join bob", []string{"join", "--agent", "bob", "--vendor", "human"}, 0)
	step("claim", []string{"claim", "src/**", "--task", "refactor", "--ttl", "10m", "--agent", "alice"}, 0)

	status := fridge(t, root, []string{"status", "--json", "--agent", "alice"})
	claims := status.JSON.ArrAt("data.claims")
	if len(claims) == 0 {
		t.Fatalf("expected a live claim, got: %s", status.Stdout)
	}
	first, _ := claims[0].(jsonx.Obj)
	claimID := first.Str("id")
	if claimID == "" {
		t.Fatalf("expected a claim id, got: %s", status.Stdout)
	}

	step("denied claim", []string{"claim", "src/api/routes.ts", "--task", "clash", "--agent", "bob"}, 10)
	step("denied claim, queued", []string{"claim", "src/api/db.ts", "--task", "clash", "--queue", "--agent", "bob"}, 10)
	step("pin", []string{"pin", "left a note on the door", "--agent", "bob"}, 0)
	step("heartbeat", []string{"heartbeat", "--agent", "alice"}, 0)
	step("extend", []string{"extend", claimID, "--ttl", "20m", "--agent", "alice"}, 0)
	step("handoff", []string{"handoff", claimID, "--to", "bob", "--note", "yours now", "--agent", "alice"}, 0)
	step("accept", []string{"accept", claimID, "--agent", "bob"}, 0)
	step("release", []string{"release", claimID, "--outcome", "done", "--agent", "bob"}, 0)
	step("reap", []string{"reap"}, 0)
}

// The first thing any agent does is join. That must not be enough to make a
// brand new workspace fail its own health check.
func TestJoinLeavesAPristineWorkspaceHealthy(t *testing.T) {
	root := makeRepo(t, "derived-init")
	fridge(t, root, []string{"init", "--no-adapters", "--quiet"})
	if code := fridge(t, root, []string{"doctor", "--check"}).Code; code != 0 {
		t.Fatalf("init alone left the workspace unhealthy: %d", code)
	}

	fridge(t, root, []string{"join", "--agent", "solo", "--vendor", "human"})
	check := fridge(t, root, []string{"doctor", "--check"})
	if check.Code != 0 {
		t.Fatalf("join made a pristine workspace unhealthy:\n%s%s", check.Stdout, check.Stderr)
	}
}

func TestDoorShowsTheJoinThatJustHappened(t *testing.T) {
	root := makeRepo(t, "derived-content")
	fridge(t, root, []string{"init", "--no-adapters", "--quiet"})
	fridge(t, root, []string{"join", "--agent", "zephyr", "--vendor", "claude"})

	if door := readDoor(t, root); !strings.Contains(door, "zephyr") {
		t.Fatalf("the door should already show the actor who just joined:\n%s", door)
	}
}
