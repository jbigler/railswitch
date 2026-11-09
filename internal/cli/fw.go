package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/firewall"
	"github.com/jbigler/railswitch/internal/naming"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/sys"
	"github.com/jbigler/railswitch/internal/workspace"
)

// fwTimeout bounds one firewall.sh run: show resolves each rejected address
// (2 s each), allow resolves each host.
const fwTimeout = 2 * time.Minute

const fwUsage = "usage: railswitch fw show [service] | railswitch fw allow [--for <duration>] <service> <host|ip>..."

// fwMaxFor bounds fw allow --for: a longer allow belongs in the profile's
// allow list.
const fwMaxFor = 7 * 24 * time.Hour

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
	var fs *flag.FlagSet
	var forDur *time.Duration
	if sub == "allow" {
		fs = e.flags("fw allow")
		forDur = fs.Duration("for", 0, "the allow expires after this long (the kernel drops it), e.g. 30m; at most 168h")
	} else {
		fs = e.flags("fw " + sub)
	}
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
	var forArgs []string
	if sub == "allow" {
		// The address floor (D179) is refused here, with the reason;
		// the hook's rules would only drop it silently.
		for _, h := range rest[1:] {
			if err := config.CheckFloor(h); err != nil {
				return e.usageErr("fw allow: %v", err)
			}
			if *forDur != 0 && strings.Contains(h, "/") {
				return e.usageErr("fw allow --for: %s is a range; a timed allow takes addresses and hostnames", h)
			}
		}
		switch d := *forDur; {
		case d < 0, d > fwMaxFor, d != 0 && d < time.Second:
			return e.usageErr("fw allow --for: %v is not between 1s and %v", d, fwMaxFor)
		case d != 0:
			forArgs = []string{"--for", strconv.Itoa(int(d.Seconds())) + "s"}
		}
	}
	w, err := workspace.Load(ctx, e.dir, e.slug)
	if err != nil {
		return e.fail(err)
	}
	if sub == "allow" && w.Slug == "" {
		return e.usageErr("fw allow: not in a worktree; run it in <workspace>/worktrees/<worktree>/ or pass -w <worktree>")
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
		Args:    append(append([]string{sub}, forArgs...), rest...),
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
	Refused  []fwRefused  `json:"refused"` // names its DNS filter refused (D195), the latest first
}

// fwRefused is a name a container's DNS filter answered "no such name"
// (D182), and how many lookups of it the filter's log still holds.
type fwRefused struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
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
// (container), R (rejected) and N (refused name) lines as one JSON document.
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
			c := fwContainer{Name: f[1], Firewall: f[2], Profiles: []string{}, Mode: f[4], Rejected: []fwRejected{}, Refused: []fwRefused{}}
			if f[3] != "" {
				c.Profiles = strings.Split(f[3], ",")
			}
			byName[c.Name] = len(doc.Containers)
			doc.Containers = append(doc.Containers, c)
		case f[0] == "R" && len(f) == 6:
			if i, ok := byName[f[1]]; ok {
				doc.Containers[i].Rejected = append(doc.Containers[i].Rejected, fwRejected{Addr: f[2], Proto: f[3], Port: f[4], Host: f[5]})
			}
		case f[0] == "N" && len(f) == 4:
			// The name came from a lookup in the container: firewall.sh passes
			// only host names, and this checks again.
			n, err := strconv.Atoi(f[3])
			if i, ok := byName[f[1]]; ok && err == nil && n > 0 && config.CheckAllowHost(f[2]) == nil && !strings.ContainsAny(f[2], "/:") {
				doc.Containers[i].Refused = append(doc.Containers[i].Refused, fwRefused{Name: f[2], Count: n})
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
