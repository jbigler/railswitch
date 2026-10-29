package prs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// Stack is a worktree branch's stack as GitHub's gh-stack extension sees
// it (`gh stack view --json`): Branches bottom (next to the trunk) to top.
type Stack struct {
	Trunk    string        `json:"trunk"`
	Current  string        `json:"current"`
	Branches []StackBranch `json:"branches"`
}

// StackBranch is one branch of a Stack; PR is nil before `gh stack submit`.
type StackBranch struct {
	Name        string   `json:"name"`
	Current     bool     `json:"current"`
	Merged      bool     `json:"merged"`
	Queued      bool     `json:"queued"`
	NeedsRebase bool     `json:"needs_rebase"`
	PR          *StackPR `json:"pr"`
}

// StackPR is a stack branch's pull request: State is "open", "merged" or
// "closed".
type StackPR struct {
	Number int    `json:"number"`
	State  string `json:"state"`
}

// StackAvailable reports whether gh has the gh-stack extension.
func StackAvailable(ctx context.Context, r sys.Runner) bool {
	res, err := r.Run(ctx, sys.Cmd{Name: "gh", Args: []string{"extension", "list"}, Dir: NeutralDir, Env: append(os.Environ(), "GH_PROMPT_DISABLED=1"), Timeout: 30 * time.Second})
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(res.Stdout)) {
		if f := strings.Fields(line); len(f) >= 2 && f[0] == "gh" && f[1] == "stack" {
			return true
		}
	}
	return false
}

// noStack is what gh stack view says for a branch outside any stack (the
// trunk included); every other failure is an error.
var noStack = []string{"is not part of a stack", "belongs to multiple stacks"}

// ViewStack runs `gh stack view --json` in worktree dir: its branch's
// stack, nil when it is in none. gh-stack runs git there, so git's hooks
// and fsmonitor are turned off through the environment (the worktree's git
// config is the project's, not trusted), and nothing may prompt.
func ViewStack(ctx context.Context, r sys.Runner, dir string) (*Stack, error) {
	res, err := r.Run(ctx, sys.Cmd{
		Name:    "gh",
		Args:    []string{"stack", "view", "--json"},
		Dir:     dir,
		Env:     stackEnv(os.Environ()),
		Timeout: 30 * time.Second,
	})
	if err != nil {
		msg := string(res.Stderr) + string(res.Stdout)
		for _, s := range noStack {
			if errors.Is(err, sys.ErrExit) && strings.Contains(msg, s) {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("gh stack view: %w", err)
	}
	s, err := parseStack(res.Stdout)
	if err != nil {
		return nil, err
	}
	// On the trunk gh stack shows the stack above it, no branch current:
	// the trunk is under the stack, not in it.
	if !slices.ContainsFunc(s.Branches, func(b StackBranch) bool { return b.Current }) {
		return nil, nil
	}
	return s, nil
}

// stackEnv is environ for gh-stack's git: its own GIT_CONFIG_* pairs
// replaced by hooks and fsmonitor off, no terminal prompt, no gh prompt.
func stackEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+8)
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(k, "GIT_CONFIG_") || k == "GIT_TERMINAL_PROMPT" || k == "GH_PROMPT_DISABLED" {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=core.hooksPath", "GIT_CONFIG_VALUE_0=/dev/null",
		"GIT_CONFIG_KEY_1=core.fsmonitor", "GIT_CONFIG_VALUE_1=false",
		"GIT_TERMINAL_PROMPT=0", "GH_PROMPT_DISABLED=1")
}

type ghStack struct {
	Trunk         string `json:"trunk"`
	CurrentBranch string `json:"currentBranch"`
	Branches      []struct {
		Name        string `json:"name"`
		IsCurrent   bool   `json:"isCurrent"`
		IsMerged    bool   `json:"isMerged"`
		IsQueued    bool   `json:"isQueued"`
		NeedsRebase bool   `json:"needsRebase"`
		PR          *struct {
			Number int    `json:"number"`
			State  string `json:"state"`
		} `json:"pr"`
	} `json:"branches"`
}

func parseStack(data []byte) (*Stack, error) {
	var g ghStack
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("gh stack view: %w", err)
	}
	s := &Stack{Trunk: g.Trunk, Current: g.CurrentBranch, Branches: []StackBranch{}}
	for _, b := range g.Branches {
		sb := StackBranch{Name: b.Name, Current: b.IsCurrent, Merged: b.IsMerged, Queued: b.IsQueued, NeedsRebase: b.NeedsRebase}
		if b.PR != nil && b.PR.Number > 0 {
			sb.PR = &StackPR{Number: b.PR.Number, State: strings.ToLower(b.PR.State)}
		}
		s.Branches = append(s.Branches, sb)
	}
	return s, nil
}
