package prs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// Limits of Recent's two queries.
const (
	recentOpen = 500
	recentAll  = 300
)

// Recent lists repo's pull requests for a branch list (branch ls): every
// open one, up to recentOpen, and the newest recentAll of any state, with
// no checks or review (a repo-wide list with checks outruns GitHub's
// GraphQL time limit). A branch whose only PR is older than those reads as
// having none. PRs from forks are left out, as List leaves them.
func Recent(ctx context.Context, r sys.Runner, repo string) ([]PR, error) {
	var out []PR
	seen := map[int]bool{}
	for _, q := range []struct {
		state string
		limit int
	}{{"open", recentOpen}, {"all", recentAll}} {
		res, err := r.Run(ctx, sys.Cmd{
			Name: "gh",
			Args: []string{"pr", "list", "--repo", repo, "--state", q.state, "--limit", strconv.Itoa(q.limit),
				"--json", "number,title,url,headRefName,headRefOid,isCrossRepository,isDraft,state,mergedAt"},
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
			if p.IsCross || seen[p.Number] {
				continue
			}
			seen[p.Number] = true
			out = append(out, PR{Number: p.Number, Title: p.Title, URL: p.URL, Branch: p.HeadRefName, Head: p.HeadRefOid, State: state(p), Merged: p.MergedAt})
		}
	}
	return out, nil
}
