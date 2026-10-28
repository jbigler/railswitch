package render

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
)

// BuildStep is one `podman build` of a worktree's images.
type BuildStep struct {
	Name    string            // "base", "addon pg-client", "nvim", "agent-base", "agent <pkg>", "playwright"
	File    string            // absolute path, always under <ws>/containerfiles/
	Context string            // always <ws>/containerfiles
	Args    map[string]string // includes BASE_IMAGE for every chain/tool/agent step after the first; never for a standalone step
	Tag     string            // D9
	From    []string          // FROM refs of this file (ARGs expanded), excluding ${BASE_IMAGE}, scratch and stage names (D36)
}

// containerfileRe is a workspace Containerfile path; only these are built (D31).
var containerfileRe = regexp.MustCompile(`^containerfiles/Containerfile\.([a-z][a-z0-9-]*)$`)

// tagPartRe is what a tag's version part may hold.
var tagPartRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,118}$`)

// containerfile is a parsed workspace Containerfile.
type containerfile struct {
	rel  string // "containerfiles/Containerfile.<name>"
	name string // "<name>"
	path string // absolute
	data []byte

	args    map[string]string // ARGs declared before the first FROM → default ("" if none)
	argList []string          // their names, in file order (a default may use an earlier ARG)
	baseArg bool              // ARG BASE_IMAGE before the first FROM
	froms   []fromLine
	user    string // the last USER
	hasUser bool
}

type fromLine struct {
	image, stage string
	bare         bool // exactly "FROM <image>": no flags, no AS
}

// images reads and checks every Containerfile the stack builds: the base
// chain, each enabled tool's, with agents agent-base and each agent
// package's, and each chosen standalone option's.
func (b *builder) images() error {
	p, st := b.in.Project, b.in.Stack
	var errs []error
	read := func(rel string) *containerfile {
		cf, err := b.readContainerfile(rel)
		if err != nil {
			errs = append(errs, err)
		}
		return cf
	}
	for i, rel := range slices.Concat([]string{p.Image.Base}, p.Image.Addons) {
		cf := read(rel)
		if cf == nil {
			continue
		}
		if i > 0 {
			errs = append(errs, cf.checkDerived())
		}
		b.m.chain = append(b.m.chain, cf)
	}
	for _, t := range st.Tools {
		if cf := read("containerfiles/" + t.Image.File); cf != nil {
			errs = append(errs, cf.checkDerived())
			b.m.tools[t.Service] = cf
		}
	}
	if len(st.Agents) > 0 {
		if cf := read("containerfiles/Containerfile.agent-base"); cf != nil {
			errs = append(errs, cf.checkDerived())
			b.m.agentBase = cf
		}
	}
	seen := map[string]bool{}
	for _, a := range st.Agents {
		pkg := agentPkg(a.Agent)
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		if cf := read("containerfiles/" + a.Agent.Image.File); cf != nil {
			errs = append(errs, cf.checkDerived())
			b.m.agents[pkg] = cf
		}
	}
	for _, ch := range st.Options {
		if ch.Option.Image.Build == "" {
			continue
		}
		if cf := read("containerfiles/" + ch.Option.Image.Build); cf != nil {
			errs = append(errs, cf.checkUser())
			b.m.standalone[ch.Service] = cf
		}
	}
	return errors.Join(errs...)
}

func (b *builder) readContainerfile(rel string) (*containerfile, error) {
	m := containerfileRe.FindStringSubmatch(rel)
	if m == nil {
		return nil, fmt.Errorf("%s: not containerfiles/Containerfile.<name>", rel)
	}
	abs := b.ws(filepath.FromSlash(rel))
	data, err := os.ReadFile(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%s is missing: run `railswitch containerfile eject %s`", rel, m[1])
	}
	if err != nil {
		return nil, err
	}
	cf := parseContainerfile(data)
	cf.rel, cf.name, cf.path, cf.data = rel, m[1], abs, data
	return cf, nil
}

// parseContainerfile reads the instructions the checks need: pre-FROM ARGs,
// FROMs and USERs. Continuation lines are joined; comments are dropped.
func parseContainerfile(data []byte) *containerfile {
	cf := &containerfile{args: map[string]string{}}
	for _, line := range instructions(data) {
		f := strings.Fields(line)
		switch strings.ToUpper(f[0]) {
		case "ARG":
			if len(cf.froms) > 0 {
				continue
			}
			for _, a := range f[1:] {
				k, v, _ := strings.Cut(a, "=")
				if _, ok := cf.args[k]; !ok {
					cf.argList = append(cf.argList, k)
				}
				cf.args[k] = strings.Trim(v, `"'`)
				if k == "BASE_IMAGE" {
					cf.baseArg = true
				}
			}
		case "FROM":
			fl := fromLine{bare: len(f) == 2}
			var parts []string
			for _, x := range f[1:] {
				if !strings.HasPrefix(x, "--") {
					parts = append(parts, x)
				}
			}
			if len(parts) > 0 {
				fl.image = parts[0]
			}
			if len(parts) >= 3 && strings.EqualFold(parts[1], "AS") {
				fl.stage = parts[2]
			}
			cf.froms = append(cf.froms, fl)
		case "USER":
			if len(f) > 1 {
				cf.user, cf.hasUser = f[1], true
			}
		}
	}
	return cf
}

// instructions returns the logical lines of a Containerfile.
func instructions(data []byte) []string {
	var out []string
	var cur strings.Builder
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimRight(l, " \t\r")
		if t := strings.TrimSpace(l); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if s, ok := strings.CutSuffix(l, `\`); ok {
			cur.WriteString(s)
			cur.WriteByte(' ')
			continue
		}
		cur.WriteString(l)
		out = append(out, strings.TrimSpace(cur.String()))
		cur.Reset()
	}
	if t := strings.TrimSpace(cur.String()); t != "" {
		out = append(out, t)
	}
	return out
}

// checkDerived is the D31 contract of every chain step after the first and
// of every addon and tool file.
func (cf *containerfile) checkDerived() error {
	var errs []error
	if !cf.baseArg {
		errs = append(errs, fmt.Errorf("%s: `ARG BASE_IMAGE` must come before the first FROM (D31)", cf.rel))
	}
	if n := len(cf.froms); n == 0 || cf.froms[n-1].image != "${BASE_IMAGE}" || !cf.froms[n-1].bare {
		errs = append(errs, fmt.Errorf("%s: the last FROM must be exactly `FROM ${BASE_IMAGE}` (D31): it builds on the previous step's image", cf.rel))
	}
	errs = append(errs, cf.checkUser())
	return errors.Join(errs...)
}

// checkUser: the image runs as uid 1000 (keep-id maps the host user to 1000).
func (cf *containerfile) checkUser() error {
	switch {
	case !cf.hasUser, cf.user == "appuser", cf.user == "1000", cf.user == "1000:1000":
		return nil
	}
	return fmt.Errorf("%s: the last USER is %q; it must be `USER appuser`, `USER 1000` or `USER 1000:1000` (keep-id maps the host user to 1000)", cf.rel, cf.user)
}

// fromRefs returns the file's FROM refs with ARGs expanded from args and the
// pre-FROM ARG defaults, excluding ${BASE_IMAGE} (when derived), scratch and
// stage names. Each must be fully qualified (D36).
func (cf *containerfile) fromRefs(args map[string]string, derived bool) ([]string, error) {
	vars := map[string]string{}
	for _, k := range cf.argList {
		if v, ok := args[k]; ok {
			vars[k] = v
			continue
		}
		v, err := expandArgs(cf.args[k], vars)
		if err != nil {
			return nil, fmt.Errorf("%s: ARG %s: %w", cf.rel, k, err)
		}
		vars[k] = v
	}
	stages := map[string]bool{}
	var out []string
	for _, f := range cf.froms {
		img := f.image
		switch {
		case derived && img == "${BASE_IMAGE}":
		case strings.EqualFold(img, "scratch"), stages[strings.ToLower(img)]:
		default:
			ref, err := expandArgs(img, vars)
			if err != nil {
				return nil, fmt.Errorf("%s: FROM %s: %w", cf.rel, img, err)
			}
			if !qualified(ref) {
				return nil, fmt.Errorf("%s: FROM %s is not fully qualified: use <registry>/<repo>:<tag> or …@sha256:<digest>; short names resolve per host registries.conf and can't be pinned (D36)", cf.rel, ref)
			}
			if !slices.Contains(out, ref) {
				out = append(out, ref)
			}
		}
		if f.stage != "" {
			stages[strings.ToLower(f.stage)] = true
		}
	}
	return out, nil
}

// expandArgs expands $NAME and ${NAME…} in s from vars, with the modifiers
// buildah takes in FROM and ARG: ${N:-word}, ${N:+word}, and the glob trims
// ${N#pat}, ${N##pat}, ${N%pat}, ${N%%pat}. Any other form is an error, so
// railswitch never pulls a ref the build would not use.
func expandArgs(s string, vars map[string]string) (string, error) {
	var err error
	out := os.Expand(s, func(k string) string {
		i := strings.IndexFunc(k, func(r rune) bool { return !(r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)) })
		if i < 0 {
			return vars[k]
		}
		name, op := k[:i], k[i:]
		v := vars[name]
		switch {
		case strings.HasPrefix(op, ":-"):
			if v == "" {
				return op[2:]
			}
			return v
		case strings.HasPrefix(op, ":+"):
			if v != "" {
				return op[2:]
			}
			return ""
		case strings.HasPrefix(op, "##"):
			return trimGlob(v, op[2:], true, true)
		case strings.HasPrefix(op, "#"):
			return trimGlob(v, op[1:], true, false)
		case strings.HasPrefix(op, "%%"):
			return trimGlob(v, op[2:], false, true)
		case strings.HasPrefix(op, "%"):
			return trimGlob(v, op[1:], false, false)
		}
		if err == nil {
			err = fmt.Errorf("${%s}: unsupported expansion (use ${N}, ${N:-word}, ${N:+word}, ${N#pat}, ${N##pat}, ${N%%pat} or ${N%%%%pat})", k)
		}
		return ""
	})
	return out, err
}

// trimGlob removes the shortest (or, with longest, the longest) prefix or
// suffix of v that matches the glob pat; v is unchanged when none does.
func trimGlob(v, pat string, prefix, longest bool) string {
	n := len(v)
	for j := 0; j <= n; j++ {
		i := j // the cut length, shortest first
		if longest {
			i = n - j
		}
		part := v[n-i:]
		if prefix {
			part = v[:i]
		}
		if ok, _ := path.Match(pat, part); ok {
			if prefix {
				return v[i:]
			}
			return v[:n-i]
		}
	}
	return v
}

var digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// qualified reports whether ref names its registry and a tag or digest.
func qualified(ref string) bool {
	name, digest, hasDigest := strings.Cut(ref, "@")
	reg, rest, ok := strings.Cut(name, "/")
	if !ok || rest == "" || (reg != "localhost" && !strings.ContainsAny(reg, ".:")) {
		return false
	}
	if hasDigest {
		return digestRe.MatchString(digest)
	}
	_, tag, ok := strings.Cut(rest[strings.LastIndex(rest, "/")+1:], ":")
	return ok && tag != ""
}

// hash8 is the first 8 hex of the sha256 of a Containerfile, its sorted
// build args (D9) and the digests stack.lock records for its FROM refs
// (D36), so a new base digest gives a new tag.
func hash8(data []byte, args map[string]string, from []string, lock *config.Lock) string {
	h := sha256.New()
	h.Write(data)
	for _, k := range sortedKeys(args) {
		h.Write([]byte{0})
		io.WriteString(h, k+"="+args[k])
	}
	if lock != nil {
		for _, r := range from {
			if d := lock.BaseDigest(r); d != "" {
				h.Write([]byte{1})
				io.WriteString(h, r+"@"+d)
			}
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:8]
}

func tagPart(what, v string) error {
	if !tagPartRe.MatchString(v) {
		return fmt.Errorf("%s: %q is not a valid image tag part", what, v)
	}
	return nil
}

// imageValues computes the worktree's builds and every image.<svc> value.
func (m *Model) imageValues(in *Instance) error {
	st := m.Inputs.Stack
	pk := st.Pack
	app := m.Services[0].ID
	ctx := filepath.Join(m.Inputs.Workspace, "containerfiles")
	ver, err := pack.Expand(pk.Image.Tag, in.Scope(app))
	if err != nil {
		return fmt.Errorf("pack image.tag: %w", err)
	}
	if err := tagPart("pack image.tag", ver); err != nil {
		return err
	}
	step := func(name string, cf *containerfile, args map[string]string, derived bool, repo, version string) (string, error) {
		from, err := cf.fromRefs(args, derived)
		if err != nil {
			return "", err
		}
		tag := fmt.Sprintf("localhost/%s/%s:%s-%s", m.Prefix, repo, version, hash8(cf.data, args, from, m.Inputs.Lock))
		in.Builds = append(in.Builds, BuildStep{Name: name, File: cf.path, Context: ctx, Args: args, Tag: tag, From: from})
		return tag, nil
	}

	base := ""
	for i, cf := range m.chain {
		args, name := map[string]string{}, "base"
		if i == 0 {
			for k, t := range pk.Image.Args {
				if args[k], err = pack.Expand(t, in.Scope(app)); err != nil {
					return fmt.Errorf("pack image.args.%s: %w", k, err)
				}
			}
		} else {
			args["BASE_IMAGE"], name = base, "addon "+cf.name
		}
		if base, err = step(name, cf, args, i > 0, "base", ver); err != nil {
			return err
		}
	}
	in.Values["image.base"] = base

	for _, t := range st.Tools {
		tag, err := step(t.Name, m.tools[t.Service], map[string]string{"BASE_IMAGE": base}, true, t.Name, ver)
		if err != nil {
			return err
		}
		in.Values["image."+t.Service] = tag
	}

	// The agent base on the chain's end, each agent package's image on it.
	if cf := m.agentBase; cf != nil {
		agentBase, err := step("agent-base", cf, map[string]string{"BASE_IMAGE": base}, true, "agent-base", ver)
		if err != nil {
			return err
		}
		in.Values["image.agent-base"] = agentBase
		tags := map[string]string{} // agent package → its image
		for _, a := range st.Agents {
			pkg := agentPkg(a.Agent)
			if tags[pkg] == "" {
				if tags[pkg], err = step("agent "+pkg, m.agents[pkg], map[string]string{"BASE_IMAGE": agentBase}, true, "agent-"+pkg, ver); err != nil {
					return err
				}
			}
			in.Values["image."+a.Service] = tags[pkg]
		}
	}

	for _, ch := range st.Options {
		if ch.Option.Image.Build == "" {
			continue
		}
		o, sc := ch.Option, in.Scope(ch.Service)
		args := map[string]string{}
		for k, t := range o.Image.Args {
			if args[k], err = pack.Expand(t, sc); err != nil {
				return fmt.Errorf("%s image.args.%s: %w", o.Name, k, err)
			}
		}
		v, err := pack.Expand(o.Image.Tag, sc)
		if err != nil {
			return fmt.Errorf("%s image.tag: %w", o.Name, err)
		}
		if err := tagPart(o.Name+" image.tag", v); err != nil {
			return err
		}
		cf := m.standalone[ch.Service]
		tag, err := step(cf.name, cf, args, false, cf.name, v)
		if err != nil {
			return err
		}
		in.Values["image."+ch.Service] = tag
	}

	for _, s := range m.Services {
		switch s.Image.Kind {
		case ImageBase:
			in.Values["image."+s.ID] = base
		case ImagePull:
			ref, err := pack.Expand(s.Image.Ref, in.Scope(s.ID))
			if err != nil {
				return fmt.Errorf("%s image: %w", s.ID, err)
			}
			if l := m.Inputs.Lock; l != nil {
				if d := l.Digest(ref); d != "" {
					ref += "@" + d
				}
			}
			in.Values["image."+s.ID] = ref
		}
	}
	return nil
}
