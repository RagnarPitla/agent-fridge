// SPDX-License-Identifier: Apache-2.0
// Cross-implementation conformance. A .fridge/ written by the Go binary must be
// readable by the Node CLI and the other way round: same filenames, same JSON
// field names, same sort order, same generated views. Skipped when node is not
// on PATH.
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RagnarPitla/agent-fridge/internal/jsonx"
)

func nodeCLI(t *testing.T) string {
	t.Helper()
	if nodeBin() == "" {
		t.Skip("node is not on PATH")
	}
	return filepath.Join(repoRoot(t), "bin", "fridge.mjs")
}

// node runs the Node reference CLI against the same workspace.
func node(t *testing.T, root string, args []string, actor string) runResult {
	t.Helper()
	cli := nodeCLI(t)
	cmd := exec.Command(nodeBin(), append([]string{cli}, args...)...)
	cmd.Dir = root
	env := append([]string{}, os.Environ()...)
	env = append(env, "FRIDGE_ACTOR="+actor, "NO_COLOR=1")
	cmd.Env = env
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running node %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	res := runResult{Code: code, Stdout: out.String(), Stderr: errBuf.String()}
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

func TestNodeReadsAWorkspaceWrittenByGo(t *testing.T) {
	nodeCLI(t)
	root := makeRepo(t, "interop-go-first")
	if r := fridge(t, root, []string{"init", "--no-adapters"}); r.Code != 0 {
		t.Fatalf("go init exited %d: %s", r.Code, r.Stderr)
	}
	fridge(t, root, []string{"join", "--agent", "alice", "--vendor", "other", "--quiet"})
	fridge(t, root, []string{"join", "--agent", "bob", "--vendor", "other", "--quiet"})
	claim := fridge(t, root, []string{"claim", "src/api/**", "--task", "written by go", "--json"}, runOpts{Actor: "alice"})
	if claim.Code != 0 {
		t.Fatalf("go claim exited %d: %s", claim.Code, claim.Stderr)
	}
	id := claim.JSON.Str("data.claimId")
	fridge(t, root, []string{"pin", "a note from the go binary"}, runOpts{Actor: "alice"})

	st := node(t, root, []string{"status", "--json"}, "bob")
	if st.Code != 0 {
		t.Fatalf("node status exited %d: %s", st.Code, st.Stderr)
	}
	claims := st.JSON.ArrAt("data.claims")
	if len(claims) != 1 {
		t.Fatalf("node sees %d claims, want 1", len(claims))
	}
	seen, _ := claims[0].(jsonx.Obj)
	if seen.Str("id") != id || seen.Str("actorName") != "alice" || seen.Str("task") != "written by go" {
		t.Errorf("node read the claim as %v", seen)
	}
	// The state key is a hash of stably-stringified state. If the two writers
	// disagreed on a single byte, sort order or number format, this would drift.
	if r := node(t, root, []string{"board", "--check"}, "bob"); r.Code != 0 {
		t.Errorf("node says the Go-rendered door has drifted, exited %d: %s", r.Code, r.Stderr)
	}
	if r := node(t, root, []string{"doctor", "--check"}, "bob"); r.Code != 0 {
		t.Errorf("node doctor found damage in a Go workspace, exited %d: %s%s", r.Code, r.Stdout, r.Stderr)
	}
	if r := node(t, root, []string{"claim", "src/api/routes.ts", "--task", "sneak"}, "bob"); r.Code != 10 {
		t.Errorf("node did not honour the Go claim, exited %d", r.Code)
	}
	if r := node(t, root, []string{"log", "--limit", "10"}, "bob"); !strings.Contains(r.Stdout, "a note from the go binary") {
		t.Errorf("node cannot read Go notes:\n%s", r.Stdout)
	}
	if r := node(t, root, []string{"release", id, "--outcome", "done"}, "alice"); r.Code != 0 {
		t.Errorf("node could not release a Go claim, exited %d: %s", r.Code, r.Stderr)
	}
	if n := len(fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "alice"}).JSON.ArrAt("data.claims")); n != 0 {
		t.Errorf("go still sees %d claims after node released", n)
	}
}

func TestGoReadsAWorkspaceWrittenByNode(t *testing.T) {
	nodeCLI(t)
	root := makeRepo(t, "interop-node-first")
	if r := node(t, root, []string{"init", "--no-adapters"}, ""); r.Code != 0 {
		t.Fatalf("node init exited %d: %s", r.Code, r.Stderr)
	}
	node(t, root, []string{"join", "--agent", "alice", "--vendor", "other", "--quiet"}, "")
	node(t, root, []string{"join", "--agent", "bob", "--vendor", "other", "--quiet"}, "")
	claim := node(t, root, []string{"claim", "src/ui/**", "--task", "written by node", "--json"}, "alice")
	if claim.Code != 0 {
		t.Fatalf("node claim exited %d: %s", claim.Code, claim.Stderr)
	}
	id := claim.JSON.Str("data.claimId")
	node(t, root, []string{"pin", "a note from the node cli"}, "alice")

	st := fridge(t, root, []string{"status", "--json"}, runOpts{Actor: "bob"})
	if st.Code != 0 {
		t.Fatalf("go status exited %d: %s", st.Code, st.Stderr)
	}
	claims := st.JSON.ArrAt("data.claims")
	if len(claims) != 1 {
		t.Fatalf("go sees %d claims, want 1", len(claims))
	}
	seen, _ := claims[0].(jsonx.Obj)
	if seen.Str("id") != id || seen.Str("actorName") != "alice" {
		t.Errorf("go read the claim as %v", seen)
	}
	if r := fridge(t, root, []string{"board", "--check"}, runOpts{Actor: "bob"}); r.Code != 0 {
		t.Errorf("go says the Node-rendered door has drifted, exited %d: %s", r.Code, r.Stderr)
	}
	if r := fridge(t, root, []string{"doctor", "--check"}, runOpts{Actor: "bob"}); r.Code != 0 {
		t.Errorf("go doctor found damage in a Node workspace, exited %d: %s%s", r.Code, r.Stdout, r.Stderr)
	}
	if r := fridge(t, root, []string{"claim", "src/ui/app.tsx", "--task", "sneak"}, runOpts{Actor: "bob"}); r.Code != 10 {
		t.Errorf("go did not honour the Node claim, exited %d", r.Code)
	}
	if r := fridge(t, root, []string{"log", "--limit", "10"}, runOpts{Actor: "bob"}); !strings.Contains(r.Stdout, "a note from the node cli") {
		t.Errorf("go cannot read Node notes:\n%s", r.Stdout)
	}
	h := fridge(t, root, []string{"handoff", id, "--to", "bob", "--note", "over to you", "--json"}, runOpts{Actor: "alice"})
	if h.Code != 0 {
		t.Fatalf("go handoff exited %d: %s", h.Code, h.Stderr)
	}
	if r := node(t, root, []string{"accept", h.JSON.Str("data.messageId")}, "bob"); r.Code != 0 {
		t.Errorf("node could not accept a Go handoff, exited %d: %s", r.Code, r.Stderr)
	}
	if r := fridge(t, root, []string{"release", id}, runOpts{Actor: "bob"}); r.Code != 0 {
		t.Errorf("go could not release after the Node accept, exited %d: %s", r.Code, r.Stderr)
	}
}

// Both writers must produce byte-identical records for the same inputs.
func TestOnDiskRecordsAreByteCompatible(t *testing.T) {
	nodeCLI(t)
	goRoot := makeRepo(t, "interop-bytes-go")
	nodeRoot := makeRepo(t, "interop-bytes-node")
	fridge(t, goRoot, []string{"init", "--no-adapters", "--quiet"})
	node(t, nodeRoot, []string{"init", "--no-adapters", "--quiet"}, "")
	fridge(t, goRoot, []string{"join", "--agent", "alice", "--vendor", "claude", "--quiet"})
	node(t, nodeRoot, []string{"join", "--agent", "alice", "--vendor", "claude", "--quiet"}, "")
	fridge(t, goRoot, []string{"claim", "src/api/**", "--task", "same input", "--ttl", "30s", "--quiet"}, runOpts{Actor: "alice"})
	node(t, nodeRoot, []string{"claim", "src/api/**", "--task", "same input", "--ttl", "30s", "--quiet"}, "alice")

	for _, rel := range []string{".fridge/VERSION", ".fridge/.gitignore", ".gitattributes"} {
		a := readFile(t, filepath.Join(goRoot, filepath.FromSlash(rel)))
		b := readFile(t, filepath.Join(nodeRoot, filepath.FromSlash(rel)))
		if a != b {
			t.Errorf("%s differs between implementations:\ngo:   %q\nnode: %q", rel, a, b)
		}
	}
	goConfig := readJSONFile(t, filepath.Join(goRoot, ".fridge", "config.json"))
	nodeConfig := readJSONFile(t, filepath.Join(nodeRoot, ".fridge", "config.json"))
	delete(goConfig, "workspaceId")
	delete(nodeConfig, "workspaceId")
	delete(goConfig, "createdAt")
	delete(nodeConfig, "createdAt")
	if jsonx.Stable(goConfig) != jsonx.Stable(nodeConfig) {
		t.Errorf("config.json differs:\ngo:\n%s\nnode:\n%s", jsonx.Stable(goConfig), jsonx.Stable(nodeConfig))
	}

	goClaim := onlyClaim(t, goRoot)
	nodeClaim := onlyClaim(t, nodeRoot)
	goKeys := sortedKeys(goClaim)
	nodeKeys := sortedKeys(nodeClaim)
	if strings.Join(goKeys, ",") != strings.Join(nodeKeys, ",") {
		t.Errorf("claim records have different fields:\ngo:   %v\nnode: %v", goKeys, nodeKeys)
	}
	for _, k := range []string{"schema", "mode", "state", "task", "ttlMs", "writer", "vendor", "actorName"} {
		if jsonx.Compact(jsonx.Obj{"v": goClaim[k]}) != jsonx.Compact(jsonx.Obj{"v": nodeClaim[k]}) {
			t.Errorf("claim.%s: go %v, node %v", k, goClaim[k], nodeClaim[k])
		}
	}
	if jsonx.Stable(goClaim.ObjAt("scope")) != jsonx.Stable(nodeClaim.ObjAt("scope")) {
		t.Errorf("scope differs:\ngo:\n%s\nnode:\n%s", jsonx.Stable(goClaim.ObjAt("scope")), jsonx.Stable(nodeClaim.ObjAt("scope")))
	}
	// The stable writer is the on-disk contract: sorted keys, two-space indent,
	// trailing newline.
	raw := readFile(t, claimPath(t, goRoot))
	if raw != jsonx.Stable(goClaim) {
		t.Errorf("the Go writer is not producing stable JSON")
	}
	rawNode := readFile(t, claimPath(t, nodeRoot))
	if rawNode != jsonx.Stable(nodeClaim) {
		t.Errorf("the Node writer disagrees with the Go stable stringifier")
	}
}

func TestJSONEnvelopesMatchBetweenImplementations(t *testing.T) {
	nodeCLI(t)
	root := makeRepo(t, "interop-envelope")
	fridge(t, root, []string{"init", "--no-adapters", "--quiet"})
	fridge(t, root, []string{"join", "--agent", "alice", "--vendor", "other", "--quiet"})

	for _, args := range [][]string{
		{"whoami", "--json"},
		{"status", "--json"},
		{"inbox", "--json"},
		{"log", "--json"},
		{"claim", "../escape", "--task", "x", "--json"},
		{"release", "clm_00000000000000000000000000", "--json"},
		{"config", "nope.nope", "--json"},
	} {
		g := fridge(t, root, args, runOpts{Actor: "alice"})
		n := node(t, root, args, "alice")
		if g.Code != n.Code {
			t.Errorf("%v: go exited %d, node exited %d", args, g.Code, n.Code)
		}
		if g.JSON == nil || n.JSON == nil {
			t.Errorf("%v: go json=%v node json=%v", args, g.JSON != nil, n.JSON != nil)
			continue
		}
		gk, nk := sortedKeys(g.JSON), sortedKeys(n.JSON)
		if strings.Join(gk, ",") != strings.Join(nk, ",") {
			t.Errorf("%v: envelope keys go %v, node %v", args, gk, nk)
		}
		for _, k := range []string{"command", "exitCode", "ok", "protocol"} {
			if jsonx.Compact(jsonx.Obj{"v": g.JSON[k]}) != jsonx.Compact(jsonx.Obj{"v": n.JSON[k]}) {
				t.Errorf("%v: %s go %v, node %v", args, k, g.JSON[k], n.JSON[k])
			}
		}
		if g.JSON["error"] != nil || n.JSON["error"] != nil {
			ge, ne := g.JSON.ObjAt("error"), n.JSON.ObjAt("error")
			if ge.Str("code") != ne.Str("code") || ge.Str("message") != ne.Str("message") {
				t.Errorf("%v: error go %v, node %v", args, ge, ne)
			}
			if strings.Join(sortedKeys(ge), ",") != strings.Join(sortedKeys(ne), ",") {
				t.Errorf("%v: error keys go %v, node %v", args, sortedKeys(ge), sortedKeys(ne))
			}
		}
	}
}

func sortedKeys(o jsonx.Obj) []string {
	out := []string{}
	for k := range o {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

func claimPath(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(root, ".fridge", "claims")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatalf("no claim in %s", dir)
	return ""
}

func onlyClaim(t *testing.T, root string) jsonx.Obj {
	t.Helper()
	return readJSONFile(t, claimPath(t, root))
}
