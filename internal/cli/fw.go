package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/firewall"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

// fwTimeout bounds one firewall.sh run: show resolves each rejected address
// (2 s each), allow resolves each host.
const fwTimeout = 2 * time.Minute

const fwUsage = "usage: railswitch fw show [service] | railswitch fw allow <service> <host|ip>..."

// railswitch fw show [svc] | fw allow <svc> <host…>: run the rendered
// firewall.sh, which inspects or adjusts the live firewall of this
// worktree's containers (show: every worktree's at the workspace root).
// fw show agent-<n> first prints the agent's allowlist: an on-demand agent
// rarely has a live container.
func cmdFw(ctx context.Context, e *env, args []string) int {
	if len(args) == 0 {
		return e.usageErr(fwUsage)
	}
	sub := args[0]
	fs := e.flags("fw " + sub)
	if code, ok := parse(fs, args[1:]); !ok {
		return code
	}
	rest := fs.Args()
	switch {
	case sub == "show" && len(rest) <= 1, sub == "allow" && len(rest) >= 2:
	default:
		return e.usageErr(fwUsage)
	}
	if len(rest) > 0 {
		if err := naming.ValidService(rest[0]); err != nil {
			return e.usageErr("fw %s: %v", sub, err)
		}
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	if sub == "allow" && w.Slug == "" {
		return e.usageErr("fw allow: not in a worktree; run it in <workspace>/<worktree>/ or pass -w <worktree>")
	}
	dir := filepath.Join(w.Root, filepath.FromSlash(firewall.Dir))
	script := filepath.Join(dir, "firewall.sh")
	if _, err := os.Stat(script); err != nil {
		return e.fail(fmt.Errorf("%w: run railswitch render", err))
	}
	env := append(slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "FW_FORMAT=") }),
		"FIREWALL_DIR="+dir,
		"PREFIX="+w.Project.Project.Prefix,
		"WORKTREE="+w.Slug,
		"SELF_FIREWALLED="+strings.Join(selfFirewalled(w.Stack), ","))
	if sub == "show" && e.json {
		return e.fwShowJSON(ctx, script, env, rest)
	}
	if sub == "show" && len(rest) == 1 && isAgent(w.Stack, rest[0]) {
		allow := filepath.Join(dir, firewall.AllowFile(rest[0]))
		data, err := os.ReadFile(allow)
		if err != nil {
			return e.fail(fmt.Errorf("%w: run railswitch render", err))
		}
		fmt.Fprintf(e.stdout, "%s allowlist (%s):\n%s", rest[0], allow, data)
	}
	_, err = e.run.Run(ctx, sys.Cmd{
		Name:    script,
		Args:    append([]string{sub}, rest...),
		Env:     env,
		Stdout:  e.stdout,
		Stderr:  e.stderr,
		Timeout: fwTimeout,
	})
	if errors.Is(err, sys.ErrExit) {
		return 1 // firewall.sh printed why
	}
	if err != nil {
		return e.fail(err)
	}
	return 0
}

// fwDoc is `fw show --json` (docs/json.md).
type fwDoc struct {
	Containers []fwContainer `json:"containers"`
}

type fwContainer struct {
	Name     string       `json:"name"`
	Firewall string       `json:"firewall"` // firewalled, self (its own), none (no hook ran)
	Profiles []string     `json:"profiles"`
	Mode     string       `json:"mode"` // enforce, log, off; "" unless firewalled
	Rejected []fwRejected `json:"rejected"`
}

// fwRejected is one address the firewall rejected (enforce) or would
// have (log), with the host it resolves back to ("": none).
type fwRejected struct {
	Addr  string `json:"addr"`
	Proto string `json:"proto"`
	Port  string `json:"port"`
	Host  string `json:"host"`
}

// fwShowJSON runs firewall.sh show with FW_FORMAT=tsv and prints its C
// (container) and R (rejected) lines as one JSON document.
func (e *env) fwShowJSON(ctx context.Context, script string, env, rest []string) int {
	var out bytes.Buffer
	_, err := e.run.Run(ctx, sys.Cmd{
		Name: script, Args: append([]string{"show"}, rest...), Env: append(env, "FW_FORMAT=tsv"),
		Stdout: &out, Stderr: e.stderr, Timeout: fwTimeout,
	})
	if errors.Is(err, sys.ErrExit) {
		return 1
	}
	if err != nil {
		return e.fail(err)
	}
	doc := fwDoc{Containers: []fwContainer{}}
	byName := map[string]int{}
	for line := range strings.Lines(out.String()) {
		f := strings.Split(strings.TrimRight(line, "\n"), "\t")
		switch {
		case f[0] == "C" && len(f) == 5:
			c := fwContainer{Name: f[1], Firewall: f[2], Profiles: []string{}, Mode: f[4], Rejected: []fwRejected{}}
			if f[3] != "" {
				c.Profiles = strings.Split(f[3], ",")
			}
			byName[c.Name] = len(doc.Containers)
			doc.Containers = append(doc.Containers, c)
		case f[0] == "R" && len(f) == 6:
			if i, ok := byName[f[1]]; ok {
				doc.Containers[i].Rejected = append(doc.Containers[i].Rejected, fwRejected{Addr: f[2], Proto: f[3], Port: f[4], Host: f[5]})
			}
		}
	}
	if err := json.NewEncoder(e.stdout).Encode(doc); err != nil {
		return e.fail(err)
	}
	return 0
}

// isAgent reports whether svc is one of st's agent services.
func isAgent(st *pack.Stack, svc string) bool {
	return slices.ContainsFunc(st.Agents, func(a *pack.ChosenAgent) bool { return a.Service == svc })
}

// selfFirewalled is the ids of st's services that run their own firewall
// (firewall = "self") and so get no hook.
func selfFirewalled(st *pack.Stack) []string {
	var ids []string
	for _, ch := range st.Options {
		if slices.Contains(ch.Option.Firewall, "self") {
			ids = append(ids, ch.Service)
		}
	}
	for _, t := range st.Tools {
		if slices.Contains(t.Firewall, "self") {
			ids = append(ids, t.Service)
		}
	}
	return ids
}
