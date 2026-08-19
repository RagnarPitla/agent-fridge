// SPDX-License-Identifier: Apache-2.0
// Shared fixtures for the cmd/fridge test binaries. Mirrors test/helpers.mjs so
// that a Go case and its Node counterpart are reading the same workspace shape.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
)

var (
	builtBin  string
	buildOnce atomic.Bool
	counter   atomic.Int64
)

func repoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate go.mod above the test directory")
		}
		dir = parent
	}
}

// binary builds cmd/fridge once per test binary. The integration and
// concurrency suites drive that executable as a real child process, because
// exit codes are the contract and only a real process proves them.
func binary(t testing.TB) string {
	t.Helper()
	if builtBin != "" {
		return builtBin
	}
	root := repoRoot(t)
	out := filepath.Join(root, ".scratch", "gotest", "bin", "fridge")
	if os.PathSeparator == '\\' {
		out += ".exe"
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatal(err)
	}
	if !buildOnce.Swap(true) {
		cmd := exec.Command(goTool(t), "build", "-o", out, "./cmd/fridge")
		cmd.Dir = root
		cmd.Env = goEnv(root)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build failed: %v\n%s", err, b)
		}
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("built binary is missing: %v", err)
	}
	builtBin = out
	return out
}

func goTool(t testing.TB) string {
	t.Helper()
	local := filepath.Join(repoRoot(t), ".toolchain", "go", "bin", "go")
	if _, err := os.Stat(local); err == nil {
		return local
	}
	return "go"
}

func goEnv(root string) []string {
	env := append([]string{}, os.Environ()...)
	env = append(env,
		"GOTOOLCHAIN=local",
		"GOMODCACHE="+filepath.Join(root, ".scratch", "gomodcache"),
		"GOCACHE="+filepath.Join(root, ".scratch", "gocache"),
	)
	return env
}

// makeRepo is a throwaway git checkout with a few files in it, under the repo
// (never the system temp directory).
func makeRepo(t *testing.T, label string) string {
	t.Helper()
	base := filepath.Join(repoRoot(t), ".scratch", "gotest", "ws")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, label+"-"+strconv36(int64(os.Getpid()))+"-"+strconv36(time.Now().UnixMilli())+"-"+strconv36(counter.Add(1)))
	files := map[string]string{
		"src/api/routes.ts": "export const routes = [];\n",
		"src/api/db.ts":     "export const db = 1;\n",
		"src/ui/app.tsx":    "export const App = () => null;\n",
		"docs/guide.md":     "# guide\n",
		"README.md":         "# demo\n",
	}
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitInit := exec.Command("git", "init", "-q")
	gitInit.Dir = root
	_ = gitInit.Run()
	t.Cleanup(func() { os.RemoveAll(root) })
	return root
}

func strconv36(n int64) string {
	if n == 0 {
		return "0"
	}
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	out := []byte{}
	for n > 0 {
		out = append([]byte{digits[n%36]}, out...)
		n /= 36
	}
	return string(out)
}

type runResult struct {
	Code   int
	Stdout string
	Stderr string
	JSON   jsonx.Obj
}

type runOpts struct {
	Actor string
	Env   []string
	Stdin string
}

// fridge runs the real binary in a real process.
func fridge(t *testing.T, root string, args []string, opts ...runOpts) runResult {
	t.Helper()
	o := runOpts{}
	if len(opts) > 0 {
		o = opts[0]
	}
	cmd := exec.Command(binary(t), args...)
	cmd.Dir = root
	env := append([]string{}, os.Environ()...)
	env = append(env, "FRIDGE_ACTOR="+o.Actor, "NO_COLOR=1")
	env = append(env, o.Env...)
	cmd.Env = env
	if o.Stdin != "" {
		cmd.Stdin = strings.NewReader(o.Stdin)
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	res := runResult{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
	for _, a := range args {
		if a == "--json" {
			if v, err := jsonx.ParseObj([]byte(res.Stdout)); err == nil {
				res.JSON = v
			}
			break
		}
	}
	return res
}

// bootstrap is init + join, the two lines every test starts with.
func bootstrap(t *testing.T, label string, actors ...string) string {
	t.Helper()
	root := makeRepo(t, label)
	fridge(t, root, []string{"init", "--no-adapters", "--quiet"})
	for _, a := range actors {
		fridge(t, root, []string{"join", "--agent", a, "--vendor", "other", "--quiet"})
	}
	return root
}

func readDoor(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".fridge", "DOOR.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func noteFiles(t *testing.T, root string) []string {
	t.Helper()
	out := []string{}
	base := filepath.Join(root, ".fridge", "notes")
	_ = filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(p) != ".json" {
			return nil
		}
		out = append(out, p)
		return nil
	})
	return out
}

func notes(t *testing.T, root string) []jsonx.Obj {
	t.Helper()
	out := []jsonx.Obj{}
	for _, f := range noteFiles(t, root) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		v, err := jsonx.ParseObj(b)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out = append(out, v)
	}
	return out
}

func readJSONFile(t *testing.T, p string) jsonx.Obj {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	v, err := jsonx.ParseObj(b)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return v
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func nodeBin() string {
	p, err := exec.LookPath("node")
	if err != nil {
		return ""
	}
	return p
}
