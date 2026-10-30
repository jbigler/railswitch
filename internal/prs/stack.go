package prs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

// Stack is a worktree branch's stack as GitHub's gh-stack extension sees
// it (`gh stack view --json`): Branches bottom (next to the trunk) to top.
type Stack struct {
	Trunk    string        `json:"trunk"`
	Current  string        `json:"current"`
	Number   int           `json:"number"` // GitHub's stack number (StackNumber); 0 before gh stack submit
	Branches []StackBranch `json:"branches"`
}

// StackNumber is the GitHub stack number gh-stack recorded for the stack
// holding branch, from its catalog <gitDir>/gh-stack (gh stack view --json
// leaves it out); 0 when there is none. The catalog is in the
// container-writable .git: read as a hostile file, only the number used.
func StackNumber(gitDir, branch string) int {
	data, err := safefile.Read(filepath.Join(gitDir, "gh-stack"), safefile.Limit)
	if err != nil {
		return 0
	}
	var cat struct {
		Stacks []struct {
			Number   int `json:"number"`
			Branches []struct {
				Branch string `json:"branch"`
			} `json:"branches"`
		} `json:"stacks"`
	}
	if json.Unmarshal(data, &cat) != nil {
		return 0
	}
	for _, s := range cat.Stacks {
		for _, b := range s.Branches {
			if b.Branch == branch && s.Number > 0 {
				return s.Number
			}
		}
	}
	return 0
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
// "queued" (in a merge queue), as gh-stack reports it, or "draft" once
// Detail found it a draft; Checks and Review are as PR's, "" until Detail
// read them (and for a merged PR).
type StackPR struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Checks string `json:"checks"`
	Review string `json:"review"`
}

// maxDetail bounds Detail's queries: the stacks come from gh-stack's
// catalog in the container-writable .git, which could list any number of
// branches; past this many, the rest show without draft, checks or review.
const maxDetail = 50

// Detail fills in draft, checks and review of each open PR of stacks
// from GitHub (List: one query per branch, each branch once, at most
// maxDetail branches).
func Detail(ctx context.Context, r sys.Runner, repo string, stacks []*Stack) error {
	var branches []string
	for _, s := range stacks {
		for _, b := range s.Branches {
			if b.PR != nil && b.PR.State == "open" && !slices.Contains(branches, b.Name) && len(branches) < maxDetail {
				branches = append(branches, b.Name)
			}
		}
	}
	if len(branches) == 0 {
		return nil
	}
	list, err := List(ctx, r, repo, branches)
	if err != nil {
		return err
	}
	for _, s := range stacks {
		for _, b := range s.Branches {
			if b.PR == nil || b.PR.State != "open" {
				continue
			}
			for _, p := range list {
				if p.Number == b.PR.Number && p.Branch == b.Name {
					if p.State == "draft" {
						b.PR.State = "draft"
					}
					b.PR.Checks, b.PR.Review = p.Checks, p.Review
				}
			}
		}
	}
	return nil
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

// notInStackExit is gh-stack's exit code for a branch in no stack.
const notInStackExit = 2

// noStack is what gh stack view says for a branch outside any stack (the
// trunk included); every other failure is an error.
var noStack = []string{"is not part of a stack", "belongs to multiple stacks"}

// ViewStack runs `gh stack view --json` in worktree dir, with env: its
// branch's stack, nil when it is in none. env must keep git off the
// worktree's own common dir (gitshadow): gh-stack writes lock files there
// and runs git with its config, which a container can write.
func ViewStack(ctx context.Context, r sys.Runner, dir string, env []string) (*Stack, error) {
	res, err := r.Run(ctx, sys.Cmd{
		Name:    "gh",
		Args:    []string{"stack", "view", "--json"},
		Dir:     dir,
		Env:     append(env, "GH_PROMPT_DISABLED=1"),
		Timeout: 30 * time.Second,
	})
	if err != nil {
		// gh-stack exits 2 for a branch in no stack (ErrNotInStack); the
		// words are a fallback for another version.
		if errors.Is(err, sys.ErrExit) && res.ExitCode == notInStackExit {
			return nil, nil
		}
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
