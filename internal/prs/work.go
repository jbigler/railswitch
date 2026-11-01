package prs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// OpenPR is one open pull request of `railswitch pr ls --all`: who it is
// for, without checks (a repo-wide list with checks outruns GitHub's
// GraphQL time limit; List reads them per worktree branch).
type OpenPR struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Branch    string   `json:"branch"`
	Draft     bool     `json:"draft"`
	Author    string   `json:"author"`    // login
	Assignees []string `json:"assignees"` // logins
	Reviewers []string `json:"reviewers"` // logins asked for a review (teams left out)
	Review    string   `json:"review"`    // "approved", "changes_requested", "pending", "none"
	Updated   string   `json:"updated"`   // as gh gives it (RFC 3339)
}

func gh(ctx context.Context, r sys.Runner, args ...string) ([]byte, error) {
	res, err := r.Run(ctx, sys.Cmd{
		Name:    "gh",
		Args:    args,
		Dir:     NeutralDir,
		Env:     append(os.Environ(), "GH_PROMPT_DISABLED=1"),
		Timeout: 30 * time.Second,
	})
	return res.Stdout, err
}

// Open runs `gh pr list` for repo's open pull requests, the 100 newest;
// PRs from forks are kept (they are for review), in NeutralDir.
func Open(ctx context.Context, r sys.Runner, repo string) ([]OpenPR, error) {
	out, err := gh(ctx, r, "pr", "list", "--repo", repo, "--state", "open", "--limit", "100",
		"--json", "number,title,url,headRefName,isDraft,author,assignees,reviewRequests,reviewDecision,updatedAt")
	if err != nil {
		return nil, err
	}
	type login struct {
		Login string `json:"login"`
	}
	var raw []struct {
		Number         int     `json:"number"`
		Title          string  `json:"title"`
		URL            string  `json:"url"`
		HeadRefName    string  `json:"headRefName"`
		IsDraft        bool    `json:"isDraft"`
		Author         login   `json:"author"`
		Assignees      []login `json:"assignees"`
		ReviewRequests []login `json:"reviewRequests"` // a team has no login
		ReviewDecision string  `json:"reviewDecision"`
		UpdatedAt      string  `json:"updatedAt"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	prs := []OpenPR{}
	for _, p := range raw {
		o := OpenPR{Number: p.Number, Title: p.Title, URL: p.URL, Branch: p.HeadRefName, Draft: p.IsDraft,
			Author: p.Author.Login, Assignees: []string{}, Reviewers: []string{}, Review: review(p.ReviewDecision), Updated: p.UpdatedAt}
		for _, a := range p.Assignees {
			o.Assignees = append(o.Assignees, a.Login)
		}
		for _, q := range p.ReviewRequests {
			if q.Login != "" {
				o.Reviewers = append(o.Reviewers, q.Login)
			}
		}
		prs = append(prs, o)
	}
	return prs, nil
}

// loginRe is a GitHub login.
var loginRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// Me is the login gh is signed in as.
func Me(ctx context.Context, r sys.Runner) (string, error) {
	out, err := gh(ctx, r, "api", "user", "--jq", ".login")
	if err != nil {
		return "", err
	}
	me := strings.TrimSpace(string(out))
	if !loginRe.MatchString(me) {
		return "", fmt.Errorf("gh api user: not a login: %q", me)
	}
	return me, nil
}

// IssueTitle is issue n's title in repo.
func IssueTitle(ctx context.Context, r sys.Runner, repo string, n int) (string, error) {
	out, err := gh(ctx, r, "issue", "view", strconv.Itoa(n), "--repo", repo, "--json", "title", "--jq", ".title")
	if err != nil {
		return "", fmt.Errorf("issue #%d: %w", n, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// IssueBranch is the branch a worktree for issue n takes: issue-<n>-<the
// title's first words>, lower case, letters and digits joined by "-", at
// most 32 characters, so the worktree's slug stays short.
func IssueBranch(n int, title string) string {
	b := "issue-" + strconv.Itoa(n)
	for _, w := range strings.FieldsFunc(strings.ToLower(title), func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9')
	}) {
		if len(b)+1+len(w) > 32 {
			break
		}
		b += "-" + w
	}
	return b
}
