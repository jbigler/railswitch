package prs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jbigler/railswitch/internal/sys"
)

// Issue is one open issue of `railswitch issue ls` (docs/json.md, D144).
type Issue struct {
	Number    int      `json:"number"`
	Title     string   `json:"title"`
	URL       string   `json:"url"`
	Labels    []string `json:"labels"`    // label names
	Assignees []string `json:"assignees"` // logins
	Author    string   `json:"author"`    // login
	Updated   string   `json:"updated"`   // as gh gives it (RFC 3339)
}

// Issues runs `gh issue list` for repo's open issues, the 100 most
// recently created (gh's order), no prompts, 30 s timeout, in NeutralDir.
func Issues(ctx context.Context, r sys.Runner, repo string) ([]Issue, error) {
	res, err := r.Run(ctx, sys.Cmd{
		Name:    "gh",
		Args:    []string{"issue", "list", "--repo", repo, "--state", "open", "--limit", "100", "--json", "number,title,url,labels,assignees,updatedAt,author"},
		Dir:     NeutralDir,
		Env:     append(os.Environ(), "GH_PROMPT_DISABLED=1"),
		Timeout: 30 * time.Second,
	})
	if err != nil {
		return nil, err
	}
	type login struct {
		Login string `json:"login"`
	}
	var raw []struct {
		Number    int     `json:"number"`
		Title     string  `json:"title"`
		URL       string  `json:"url"`
		UpdatedAt string  `json:"updatedAt"`
		Author    login   `json:"author"`
		Assignees []login `json:"assignees"`
		Labels    []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(res.Stdout, &raw); err != nil {
		return nil, fmt.Errorf("gh issue list: %w", err)
	}
	out := []Issue{}
	for _, i := range raw {
		is := Issue{Number: i.Number, Title: i.Title, URL: i.URL, Author: i.Author.Login, Updated: i.UpdatedAt, Labels: []string{}, Assignees: []string{}}
		for _, l := range i.Labels {
			is.Labels = append(is.Labels, l.Name)
		}
		for _, a := range i.Assignees {
			is.Assignees = append(is.Assignees, a.Login)
		}
		out = append(out, is)
	}
	return out, nil
}
