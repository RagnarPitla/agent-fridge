// SPDX-License-Identifier: Apache-2.0
package commands

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/fsx"
)

func TestRunSleepHelperProcess(t *testing.T) {
	if os.Getenv("RUN_SLEEP_HELPER") != "1" {
		t.Skip("not the run helper")
	}
	time.Sleep(1100 * time.Millisecond)
}

func TestRunWaitsForInFlightHeartbeatBeforeRelease(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	t.Setenv("FRIDGE_ACTOR", "alice")
	t.Setenv("FRIDGE_TEST", "1")
	t.Setenv("FRIDGE_FAULT", "delay-run-heartbeat")
	t.Setenv("RUN_SLEEP_HELPER", "1")

	if code := Main([]string{"init", "--no-adapters", "--quiet"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("init exited %d", code)
	}
	if code := Main([]string{"join", "--agent", "alice", "--vendor", "other", "--quiet"}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("join exited %d", code)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	code := Main([]string{
		"run", "--claim", "src/**", "--task", "heartbeat shutdown", "--ttl", "3s",
		"--", self, "-test.run=^TestRunSleepHelperProcess$",
	}, io.Discard, io.Discard)
	if code != 0 {
		t.Fatalf("run exited %d", code)
	}
	if !fsx.Exists(filepath.Join(root, ".fridge", "tmp", "run-heartbeat-entered")) {
		t.Fatal("the regression seam never ran")
	}
	if got := fsx.ListJSON(filepath.Join(root, ".fridge", "claims")); len(got) != 0 {
		t.Fatalf("run left %d claims", len(got))
	}
	if got := fsx.ListJSON(filepath.Join(root, ".fridge", "leases")); len(got) != 0 {
		t.Fatalf("run left %d leases", len(got))
	}
	time.Sleep(900 * time.Millisecond)
	if got := fsx.ListJSON(filepath.Join(root, ".fridge", "leases")); len(got) != 0 {
		t.Fatalf("an in-flight heartbeat recreated %d leases after release", len(got))
	}
}

func TestWindowsBareCommandSearchUsesOnlyPathDirectories(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	trusted := filepath.Join(root, "trusted")
	if err := os.MkdirAll(checkout, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(trusted, 0o755); err != nil {
		t.Fatal(err)
	}
	shadow := filepath.Join(checkout, "fridge-path-probe.cmd")
	expected := filepath.Join(trusted, "fridge-path-probe.cmd")
	if err := os.WriteFile(shadow, []byte("exit /b 23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(expected, []byte("exit /b 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(checkout); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	got, ok := resolveWindowsBare("fridge-path-probe", []string{trusted}, []string{".cmd"})
	if !ok || got != expected {
		t.Fatalf("resolved %q, %v; want PATH target %q", got, ok, expected)
	}
}
