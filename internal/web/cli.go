package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CLI runs `railswitch <args>` (argv[0] is the dashboard's own executable)
// and returns stdout; a non-zero exit is an error with the stderr tail.
// The dashboard reads everything through it, so scripts see exactly what
// the page sees.
type CLI interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
	// RunInput is Run with stdin written to the command (D86: settings
	// edits and secret values travel only this way, never in argv).
	RunInput(ctx context.Context, stdin []byte, args ...string) ([]byte, error)
}

// The JSON documents of the read commands, as docs/json.md has them; only
// the fields the page uses.

type projectRow struct {
	Prefix           string `json:"prefix"`
	Workspace        string `json:"workspace"`
	Worktrees        int    `json:"worktrees"`
	WorkspaceMissing bool   `json:"workspace_missing"`
}

type overview struct {
	Prefix         string          `json:"prefix"`
	Workspace      string          `json:"workspace"`
	ReviewPending  bool            `json:"review_pending"` // a package update waits for lock --review
	SignerWarnings []signerWarning `json:"signer_warnings"`
	Template       *templateStamp  `json:"template"`        // the template the project was made from; nil: none
	TemplateUpdate bool            `json:"template_update"` // that template changed since
	Worktrees      []worktree      `json:"worktrees"`
}

// signerWarning is a locked library package whose signer is no longer
// trusted for its library.
type signerWarning struct {
	Ref         string `json:"ref"`
	Library     string `json:"library"`
	Fingerprint string `json:"fingerprint"`
}

type worktree struct {
	Slug     string    `json:"slug"`
	ID       int       `json:"id"`
	Branch   string    `json:"branch"`
	Base     bool      `json:"base"`
	Stack    string    `json:"stack"`
	Stale    bool      `json:"stale"`
	Services []service `json:"services"`
	Agent    *agent    `json:"agent"`
	Links    []link    `json:"links"`
}

type service struct {
	Service string `json:"service"`
	Role    string `json:"role"`
	State   string `json:"state"`
}

type agent struct {
	Name    string `json:"name"`
	State   string `json:"state"`
	Message string `json:"message"`
	Reply   bool   `json:"reply"`
	Running bool   `json:"running"`
}

type link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Group string `json:"group"`
}

type prRow struct {
	Slug   string `json:"slug"`
	Branch string `json:"branch"`
	PR     *pr    `json:"pr"`
}

type pr struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	State  string `json:"state"`
	Checks string `json:"checks"`
	Review string `json:"review"`
}

type task struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Source  string `json:"source"`
}

type wtRow struct {
	Slug  string `json:"slug"`
	Dir   string `json:"dir"`
	Dirty bool   `json:"dirty"`
}

type colorsMode struct {
	Base      string            `json:"base"`
	Overrides map[string]string `json:"overrides"`
	Colors    map[string]string `json:"colors"`
}

type colorsDoc struct {
	Light    colorsMode `json:"light"`
	Dark     colorsMode `json:"dark"`
	Warnings struct {
		Light []string `json:"light"`
		Dark  []string `json:"dark"`
	} `json:"warnings"`
}

// projectShow is `project show`: what the settings page edits. File is
// project.toml's text, for the raw editor.
type projectShow struct {
	Prefix     string        `json:"prefix"`
	Name       string        `json:"name"`
	Repo       string        `json:"repo"`
	BaseBranch string        `json:"base_branch"`
	Hash       string        `json:"hash"`
	Pack       packRow       `json:"pack"`
	Firewall   firewallShow  `json:"firewall"`
	Hooks      hooksShow     `json:"hooks"`
	Options    []optionRow   `json:"options"`
	Env        []envRow      `json:"env"`
	Links      []projectLink `json:"links"`
	AddChoices []string      `json:"add_choices"`
	Used       []string      `json:"used"` // the stack's pack, option and agent refs
	File       string        `json:"file"`
}

// hooksShow is project show's hooks (D126): each list's commands.
type hooksShow struct {
	Deps   []string `json:"deps"`
	Start  []string `json:"start"`
	Create []string `json:"create"`
}

// firewallShow is project show's firewall (D119).
type firewallShow struct {
	Mode      string              `json:"mode"`
	Default   string              `json:"default"`
	Effective string              `json:"effective"`
	Allow     map[string][]string `json:"allow"`
}

// packRow is the stack's pack and the packs with its app service.
type packRow struct {
	Ref     string   `json:"ref"`
	Choices []string `json:"choices"`
}

type optionRow struct {
	Key     string   `json:"key"`
	Slot    bool     `json:"slot"`
	Ref     string   `json:"ref"`
	Choices []string `json:"choices"`
}

type envRow struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type projectLink struct {
	Label  string `json:"label"`
	URL    string `json:"url"`
	From   string `json:"from"` // "project", or the stack part that brings it
	Hidden bool   `json:"hidden"`
}

// previewDoc is `settings preview`: each staged edit's impact.
type previewDoc struct {
	Hash    string       `json:"hash"`
	Diff    string       `json:"diff"`
	Edits   []editResult `json:"edits"`
	Restart []string     `json:"restart"`
	Install *reviewDoc   `json:"install"` // the library packages the edits install; nil: none
}

type editResult struct {
	Index   int      `json:"index"`
	Impact  string   `json:"impact"` // none, check, data or blocked
	Reasons []string `json:"reasons"`
	Error   string   `json:"error"`
}

// secretRow is one `secret ls` row; it never holds a value.
type secretRow struct {
	Name     string   `json:"name"`
	Scope    string   `json:"scope"`
	Delivery string   `json:"delivery"`
	Set      bool     `json:"set"`
	Created  *string  `json:"created"`
	Services []string `json:"services"`
	Up       bool     `json:"up"` // up refuses to start while it is unset
}

// detectDoc is `project detect`: the pack and options a repository
// suggests, each with its evidence. Pack nil: no pack matched.
type detectDoc struct {
	Branch  string        `json:"branch"`
	Pack    *packMatch    `json:"pack"`
	Options []optionMatch `json:"options"`
}

type packMatch struct {
	Ref      string   `json:"ref"`
	Evidence []string `json:"evidence"`
	Others   []string `json:"others"`
}

type optionMatch struct {
	Key      string   `json:"key"`
	Ref      string   `json:"ref"`
	Source   string   `json:"source"` // repo or default
	Evidence string   `json:"evidence"`
	Others   []string `json:"others"`
}

// catalogDoc is `pack ls`: what a new project chooses from. Library is a
// library package's alias, "" for the others.
type catalogDoc struct {
	Packs []struct {
		Ref     string `json:"ref"`
		Name    string `json:"name"`
		Library string `json:"library"`
	} `json:"packs"`
	Options []catalogOption `json:"options"`
	Agents  []struct {
		Ref     string `json:"ref"`
		Name    string `json:"name"`
		Default bool   `json:"default"`
		Library string `json:"library"`
	} `json:"agents"`
}

type catalogOption struct {
	Ref       string   `json:"ref"`
	Name      string   `json:"name"`
	Slot      string   `json:"slot"`
	WorksWith []string `json:"works_with"`
	Library   string   `json:"library"`
}

// dryRunDoc is `project new --dry-run`: the project.toml it would write
// and what its base worktree would get.
type dryRunDoc struct {
	ProjectTOML string `json:"project_toml"`
	Worktree    struct {
		Slug   string `json:"slug"`
		Routes []struct {
			Host    string `json:"host"`
			Service string `json:"service"`
		} `json:"routes"`
		Containers []struct {
			Name string `json:"name"`
			Role string `json:"role"`
		} `json:"containers"`
		Env []string `json:"env"`
	} `json:"worktree"`
	Warnings []string   `json:"warnings"`
	Steps    []string   `json:"steps"`
	Install  *reviewDoc `json:"install"` // the library packages it installs; nil: none
	Template *struct {
		Ref    string        `json:"ref"`
		SHA256 string        `json:"sha256"`
		Ask    []templateAsk `json:"ask"`
	} `json:"template"` // nil: not from a template
}

// libraryDoc is `library ls`: the machine's package libraries.
type libraryDoc struct {
	Libraries []libraryRow `json:"libraries"`
}

type libraryRow struct {
	Alias    string          `json:"alias"`
	URL      string          `json:"url"`
	Branch   string          `json:"branch"`
	ID       string          `json:"id"`
	Status   string          `json:"status"` // the last fetch's: trusted, unknown-signer, rotation, rolled-back, …
	Serial   int             `json:"serial"`
	Commit   string          `json:"commit"`
	Fetched  string          `json:"fetched"`
	Packages int             `json:"packages"`
	Pending  *libraryPending `json:"pending"`
}

// libraryPending is a fetch waiting for its signer to be trusted. For a
// rotation Fingerprint is the trusted key and NewFingerprint the key it
// vouches for. KnownLibrary: the library trusts other keys, and none
// vouched for this one.
type libraryPending struct {
	Fingerprint    string `json:"fingerprint"`
	NewFingerprint string `json:"new_fingerprint"`
	Status         string `json:"status"` // unknown-signer or rotation
	KnownLibrary   bool   `json:"known_library"`
}

// trustFingerprint is the key `library trust` takes for p: a rotation's
// new key, else the signing key.
func (p libraryPending) trustFingerprint() string {
	if p.Status == "rotation" {
		return p.NewFingerprint
	}
	return p.Fingerprint
}

func readCatalog(ctx context.Context, c CLI) (catalogDoc, error) {
	return readJSON[catalogDoc](ctx, c, "pack", "ls")
}

// readJSON runs `railswitch --json <args>` and decodes its document.
func readJSON[T any](ctx context.Context, c CLI, args ...string) (T, error) {
	var v T
	out, err := c.Run(ctx, append([]string{"--json"}, args...)...)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return v, fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return v, nil
}

func readProjects(ctx context.Context, c CLI) ([]projectRow, error) {
	return readJSON[[]projectRow](ctx, c, "project", "ls")
}

func readOverview(ctx context.Context, c CLI, ws string) (overview, error) {
	return readJSON[overview](ctx, c, "-C", ws, "overview")
}

func readPRs(ctx context.Context, c CLI, ws string) ([]prRow, error) {
	return readJSON[[]prRow](ctx, c, "-C", ws, "pr", "ls")
}

func readTasks(ctx context.Context, c CLI, ws, slug string) ([]task, error) {
	return readJSON[[]task](ctx, c, "-C", ws, "-w", slug, "task", "ls")
}

func readWorktrees(ctx context.Context, c CLI, ws string) ([]wtRow, error) {
	return readJSON[[]wtRow](ctx, c, "-C", ws, "wt", "ls")
}

func readColors(ctx context.Context, c CLI) (colorsDoc, error) {
	return readJSON[colorsDoc](ctx, c, "dashboard", "colors")
}

func readProjectShow(ctx context.Context, c CLI, ws string) (projectShow, error) {
	return readJSON[projectShow](ctx, c, "-C", ws, "project", "show")
}

func readSecrets(ctx context.Context, c CLI, ws string) ([]secretRow, error) {
	return readJSON[[]secretRow](ctx, c, "-C", ws, "secret", "ls")
}

// readPreview runs `settings preview` with the edit list on stdin.
func readPreview(ctx context.Context, c CLI, ws string, edits []byte) (previewDoc, error) {
	var v previewDoc
	out, err := c.RunInput(ctx, edits, "--json", "-C", ws, "settings", "preview")
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return v, fmt.Errorf("settings preview: %w", err)
	}
	return v, nil
}

// buildChain is `containerfile list`: a worktree's image builds in order.
type buildChain struct {
	Steps    []buildStep `json:"steps"`
	Warnings []string    `json:"warnings"`
}

type buildStep struct {
	Name string   `json:"name"` // base, addon <n>, a tool, agent-base, agent <pkg>, or an option image
	File string   `json:"file"` // containerfiles/Containerfile.<name>
	Tag  string   `json:"tag"`
	From []string `json:"from"`
}

// containerfileDoc is `containerfile show <name>`: the file, and the
// built-in default it was ejected from (nil: none).
type containerfileDoc struct {
	Name    string `json:"name"`
	File    string `json:"file"`
	Kind    string `json:"kind"`
	Text    string `json:"text"`
	Hash    string `json:"hash"`
	Default *struct {
		Ref string `json:"ref"`
	} `json:"default"`
	DefaultChanged bool   `json:"default_changed"`
	Diff           string `json:"diff"`
}

// checkDoc is a write's --dry-run (`containerfile write`, `pack file
// write`); Used is the package write's only.
type checkDoc struct {
	Hash    string      `json:"hash"`
	Used    bool        `json:"used"`
	Steps   []tagChange `json:"steps"`
	Restart []string    `json:"restart"`
	Errors  []string    `json:"errors"`
}

type tagChange struct {
	Worktree string `json:"worktree"`
	Name     string `json:"name"`
	OldTag   string `json:"old_tag"`
	NewTag   string `json:"new_tag"`
}

// packDoc is `pack show <ref>`: one package, with a local one's files.
type packDoc struct {
	Ref    string `json:"ref"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
	Path   string `json:"path"`
	App    *struct {
		Service string `json:"service"`
		Server  string `json:"server"`
		Port    int    `json:"port"`
		Health  string `json:"health"`
	} `json:"app"`
	Slot     string `json:"slot"`
	Service  string `json:"service"`
	Runtimes []struct {
		Name     string   `json:"name"`
		From     []string `json:"from"`
		Fallback string   `json:"fallback"`
	} `json:"runtimes"`
	Ports []struct {
		Name      string `json:"name"`
		Base      int    `json:"base"`
		Container int    `json:"container"`
	} `json:"ports"`
	Pin *struct {
		Name    string `json:"name"`
		Default string `json:"default"`
	} `json:"pin"`
	Gives  []string `json:"gives"`
	Readme string   `json:"readme"`
	Files  []struct {
		Path       string `json:"path"`
		Size       int64  `json:"size"`
		Executable bool   `json:"executable"`
	} `json:"files"`
}

// packFileDoc is `pack file show <ref> <path>`.
type packFileDoc struct {
	Path string `json:"path"`
	Text string `json:"text"`
	Hash string `json:"hash"`
}

func readChain(ctx context.Context, c CLI, ws, slug string) (buildChain, error) {
	return readJSON[buildChain](ctx, c, "-C", ws, "-w", slug, "containerfile", "list")
}

func readContainerfile(ctx context.Context, c CLI, ws, name string) (containerfileDoc, error) {
	return readJSON[containerfileDoc](ctx, c, "-C", ws, "containerfile", "show", name)
}

// readCheck runs a write's --dry-run with text on stdin; args follow
// --json.
func readCheck(ctx context.Context, c CLI, text []byte, args ...string) (checkDoc, error) {
	var v checkDoc
	out, err := c.RunInput(ctx, text, append([]string{"--json"}, args...)...)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return v, fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return v, nil
}
