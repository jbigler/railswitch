package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/jbigler/railswitch/internal/detect"
	"github.com/jbigler/railswitch/internal/pack"
	"github.com/jbigler/railswitch/internal/safefile"
	"github.com/jbigler/railswitch/internal/sys"
)

const projectDetectUsage = "usage: railswitch project detect <repo> [--branch <b>] [--pack <ref>]"

// railswitch project detect <repo> [--branch <b>] [--pack <ref>] (D88):
// the pack and options the repository's files suggest, with evidence.
// Only built-in packages are considered: before project new there is no
// workspace to hold local ones.
func projectDetect(ctx context.Context, e *env, args []string) int {
	fs := e.flags("project detect")
	branch := fs.String("branch", "", "the branch to read (default: the remote's HEAD)")
	packRef := fs.String("pack", "", "the pack to use instead of the detected one")
	repo, code, ok := parseNamed(e, fs, args, projectDetectUsage)
	if !ok {
		return code
	}
	if err := sys.GitArg("repository", repo); err != nil {
		return e.usageErr("project detect: %v", err)
	}
	if *branch != "" {
		if err := sys.GitArg("--branch", *branch); err != nil {
			return e.usageErr("project detect: %v", err)
		}
	}
	cat, err := pack.LoadBuiltins()
	if err != nil {
		return e.fail(err)
	}
	// Choose on no files checks --pack before anything touches the network.
	if _, err := detect.Choose(cat, nil, *packRef); err != nil {
		return e.fail(fmt.Errorf("project detect: --pack: %w", err))
	}
	b := *branch
	if b == "" {
		if b, err = remoteHead(ctx, e.run, repo); err != nil {
			return e.fail(fmt.Errorf("project detect: %w", err))
		}
	}
	files, err := detect.Fetch(ctx, e.run, repo, b, detect.Paths(cat))
	if err != nil {
		return e.fail(fmt.Errorf("project detect: %w", err))
	}
	res, err := detect.Choose(cat, files, *packRef)
	if err != nil {
		return e.fail(err)
	}
	res.Branch = b
	if e.json {
		if err := json.NewEncoder(e.stdout).Encode(res); err != nil {
			return e.fail(err)
		}
		return 0
	}
	tw := tabwriter.NewWriter(e.stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "branch\t%s\t\n", safefile.Printable(b))
	if res.Pack == nil {
		fmt.Fprintf(tw, "pack\tnone\tno pack's files are all in the repository\n")
	} else {
		fmt.Fprintf(tw, "pack\t%s\t%s\n", res.Pack.Ref, strings.Join(res.Pack.Evidence, ", "))
	}
	for _, o := range res.Options {
		ev := o.Evidence
		if o.Source == "default" {
			ev = "(pack default)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", o.Key, o.Ref, ev)
	}
	if err := tw.Flush(); err != nil {
		return e.fail(err)
	}
	return 0
}
