// Package systest provides a scriptable fake sys.Runner for unit tests.
package systest

import (
	"bytes"
	"context"
	"io"
	"slices"

	"github.com/jbigler/railswitch/internal/sys"
)

type rule struct {
	prefix []string
	res    sys.Result
	err    error
}

// Fake records every call and answers from scripted rules.
type Fake struct {
	Passthrough []string   // binary names run for real (e.g. "git")
	Calls       [][]string // argv of each call, Name first
	Cmds        []sys.Cmd  // full Cmd of each call
	Stdins      [][]byte   // stdin of each call; nil when Cmd.Stdin is nil
	rules       []rule
}

// On scripts the answer for calls whose argv starts with prefix. The
// longest matching prefix wins; unmatched calls get Result{} and nil.
func (f *Fake) On(prefix []string, r sys.Result, err error) {
	f.rules = append(f.rules, rule{prefix: slices.Clone(prefix), res: r, err: err})
}

// Run implements sys.Runner. Like sys.Exec, scripted output goes to
// c.Stdout/c.Stderr when set, and is returned in Result otherwise.
func (f *Fake) Run(ctx context.Context, c sys.Cmd) (sys.Result, error) {
	argv := append([]string{c.Name}, c.Args...)
	f.Calls = append(f.Calls, argv)
	f.Cmds = append(f.Cmds, c)
	var stdin []byte
	if c.Stdin != nil {
		b, err := io.ReadAll(c.Stdin)
		if err != nil {
			return sys.Result{}, err
		}
		stdin = b
		c.Stdin = bytes.NewReader(b)
	}
	f.Stdins = append(f.Stdins, stdin)
	if slices.Contains(f.Passthrough, c.Name) {
		return sys.Exec{}.Run(ctx, c)
	}
	best := -1
	for i, r := range f.rules {
		if len(r.prefix) <= len(argv) && slices.Equal(r.prefix, argv[:len(r.prefix)]) &&
			(best < 0 || len(r.prefix) >= len(f.rules[best].prefix)) {
			best = i
		}
	}
	if best < 0 {
		return sys.Result{}, nil
	}
	res, err := f.rules[best].res, f.rules[best].err
	if c.Stdout != nil {
		if _, werr := c.Stdout.Write(res.Stdout); werr != nil {
			return sys.Result{}, werr
		}
		res.Stdout = nil
	}
	if c.Stderr != nil {
		if _, werr := c.Stderr.Write(res.Stderr); werr != nil {
			return sys.Result{}, werr
		}
		res.Stderr = nil
	}
	return res, err
}
