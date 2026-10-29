// Package prs reads a GitHub repository's pull requests through the host's
// gh CLI and reduces them to draft/open/merged/closed, check and review
// states.
package prs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// PR is one pull request.
type PR struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	Branch string `json:"-"`
	Head   string `json:"-"`      // the PR's head commit
	State  string `json:"state"`  // "draft", "open", "merged", "closed"
	Checks string `json:"checks"` // "pass", "fail", "pending", "none"
	Review string `json:"review"` // "approved", "changes_requested", "pending", "none"
}

// Repo returns "owner/name" for a GitHub remote URL (https, scp-style or
// ssh://), with any ".git" suffix stripped.
func Repo(url string) (string, error) {
	var path string
	found := false
	for _, p := range []string{"https://github.com/", "git@github.com:", "ssh://git@github.com/"} {
		if rest, ok := strings.CutPrefix(url, p); ok {
			path, found = rest, true
			break
		}
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	owner, name, ok := strings.Cut(path, "/")
	if !found || !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return "", fmt.Errorf("%s is not a GitHub repo", url)
	}
	return owner + "/" + name, nil
}

type ghCheck struct {
	Typename   string `json:"__typename"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

type ghPR struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	URL            string    `json:"url"`
	HeadRefName    string    `json:"headRefName"`
	HeadRefOid     string    `json:"headRefOid"`
	IsCross        bool      `json:"isCrossRepository"`
	IsDraft        bool      `json:"isDraft"`
	State          string    `json:"state"`
	ReviewDecision string    `json:"reviewDecision"`
	Checks         []ghCheck `json:"statusCheckRollup"`
}

// NeutralDir is where railswitch runs gh when it names the repo itself:
// never in a workspace, whose mise.local.toml (D17) a mise-shimmed gh would
// read as its own config and refuse as untrusted.
const NeutralDir = "/"

// List runs `gh pr list --head=<branch>` for each of branches in repo (no
// prompts, 30 s timeout each) in NeutralDir. One query per branch: a
// repo-wide list of PRs with their checks outruns GitHub's GraphQL time
// limit (HTTP 504) on a busy repo, and only worktree branches matter.
// PRs from forks are left out: their head branch names a branch of another
// repository, which a worktree's same-named branch is not.
func List(ctx context.Context, r sys.Runner, repo string, branches []string) ([]PR, error) {
	var out []PR
	seen := map[string]bool{}
	for _, b := range branches {
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		res, err := r.Run(ctx, sys.Cmd{
			Name: "gh",
			Args: []string{"pr", "list", "--repo", repo, "--head=" + b, "--state", "all", "--limit", "20",
				"--json", "number,title,url,headRefName,headRefOid,isCrossRepository,isDraft,state,reviewDecision,statusCheckRollup"},
			Dir:     NeutralDir,
			Env:     append(os.Environ(), "GH_PROMPT_DISABLED=1"),
			Timeout: 30 * time.Second,
		})
		if err != nil {
			return nil, err
		}
		var raw []ghPR
		if err := json.Unmarshal(res.Stdout, &raw); err != nil {
			return nil, fmt.Errorf("gh pr list: %w", err)
		}
		for _, p := range raw {
			if p.IsCross {
				continue
			}
			out = append(out, PR{
				Number: p.Number,
				Title:  p.Title,
				URL:    p.URL,
				Branch: p.HeadRefName,
				Head:   p.HeadRefOid,
				State:  state(p),
				Checks: checks(p.Checks),
				Review: review(p.ReviewDecision),
			})
		}
	}
	return out, nil
}

func state(p ghPR) string {
	switch p.State {
	case "MERGED":
		return "merged"
	case "CLOSED":
		return "closed"
	}
	if p.IsDraft {
		return "draft"
	}
	return "open"
}

func checks(cs []ghCheck) string {
	if len(cs) == 0 {
		return "none"
	}
	pending := false
	for _, c := range cs {
		if c.Typename == "StatusContext" {
			switch c.State {
			case "FAILURE", "ERROR":
				return "fail"
			case "PENDING", "EXPECTED":
				pending = true
			}
			continue
		}
		switch c.Conclusion {
		case "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE":
			return "fail"
		}
		if c.Status != "COMPLETED" {
			pending = true
		}
	}
	if pending {
		return "pending"
	}
	return "pass"
}

func review(d string) string {
	switch d {
	case "APPROVED":
		return "approved"
	case "CHANGES_REQUESTED":
		return "changes_requested"
	case "REVIEW_REQUIRED":
		return "pending"
	}
	return "none"
}

// ForBranch picks the PR for branch, whose commit is head: an open or
// draft one first, then the highest number. A merged or closed PR counts
// only while the branch is still at its head commit: a branch name reused
// later (a long-lived master someone once opened a PR from) has moved on.
// It returns nil when the branch has none.
func ForBranch(prs []PR, branch, head string) *PR {
	var best *PR
	for i := range prs {
		p := &prs[i]
		if p.Branch != branch || !live(p) && (head == "" || p.Head != head) {
			continue
		}
		if best == nil || live(p) && !live(best) || live(p) == live(best) && p.Number > best.Number {
			best = p
		}
	}
	return best
}

func live(p *PR) bool { return p.State == "open" || p.State == "draft" }
