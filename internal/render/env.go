package render

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jbigler/railswitch/internal/config"
	"github.com/jbigler/railswitch/internal/pack"
)

// Layer is an env layer (HANDOFF §3.7), lowest first: a higher layer wins.
type Layer int

const (
	LayerPack     Layer = iota + 1 // the pack's [env] (and, for deps, [deps] frozen_env)
	LayerOption                    // options' [gives] and a service's own option/tool [env]
	LayerProject                   // project.toml [env]
	LayerService                   // project.toml [services.<id>.env]
	LayerWorktree                  // WORKTREE_*, published port numbers
	LayerSecret                    // git wiring for secrets with git_host (D33)
	LayerShare                     // [share] env/gives, only while the worktree is shared (D46)
)

var layerNames = [...]string{LayerPack: "pack", LayerOption: "option", LayerProject: "project", LayerService: "service", LayerWorktree: "worktree", LayerSecret: "secret", LayerShare: "share"}

func (l Layer) String() string {
	if l >= LayerPack && l <= LayerShare {
		return layerNames[l]
	}
	return "layer(" + strconv.Itoa(int(l)) + ")"
}

// EnvVar is one env var: a template in Service.EnvSpec, a concrete value in
// Instance.Env.
type EnvVar struct {
	Name, Value string
	Layer       Layer
	Source      string // where it was declared, e.g. "option postgres@1 [gives]"

	svc, scheme string // {service} and {scheme} for Value: the giving option's for [gives]
}

// scope is a per-service placeholder scope (pack.Scope).
type scope struct {
	values      map[string]string
	svc, scheme string
	gen         map[string]string // generated values; nil outside env
}

func (s scope) Lookup(name string) (string, bool) {
	switch name {
	case "service":
		return s.svc, s.svc != ""
	case "scheme":
		return s.scheme, s.scheme != ""
	}
	if g, ok := strings.CutPrefix(name, "generated."); ok {
		v := s.gen[g] // empty = missing: never postgres://postgres:@db/…
		return v, v != ""
	}
	v, ok := s.values[name]
	return v, ok
}

func optionSource(o *pack.Option) string {
	return "option " + pack.Ref{Name: o.Name, Version: o.Version}.String()
}

// envSpecs fills each service's EnvSpec, lowest layer first (HANDOFF §3.7;
// D10, D11 decide who gets what):
//   - pack [env]: app, worker, deps, tool packages (nvim) and agents; deps
//     also gets [deps] frozen_env;
//   - options' [gives]: app, worker, tool packages and agents, never the
//     giver itself and never a role-tool option (the browser): least
//     privilege, it needs no data-service credentials;
//   - options' [deps_env]: deps (D44);
//   - options' [agent_gives]: every agent (C6), e.g. BROWSER_CDP_URL (D59);
//   - an option's, tool's or agent package's own [env]: its own service;
//   - project [env]: every service but deps;
//   - [agents.<n>] env: that agent, at layer project, over project [env];
//   - [tools.nvim] config_repo/config_ref: nvim, at layer project, as
//     NVIM_CONFIG_REPO/NVIM_CONFIG_REF when set (D54);
//   - [services.<id>.env]: that service;
//   - worktree: WORKTREE_ID/SLUG/HOST to app, worker, every tool service
//     (tool options included: the browser's entrypoint needs WORKTREE_HOST)
//     and every agent, plus every service's own published ports (deps
//     publishes none); an agent also gets RAILSWITCH_AGENT and
//     RAILSWITCH_STATUS_FILE (D58);
//   - secret: git wiring for the services of a secret with git_host.
//
// Layer 7 goes to ShareEnv, never EnvSpec: the pack's [share] env and the
// options' [share] gives reach app, worker, tool packages and agents, project
// [share] env every service but deps (D46). deps gets no layer 7 (D11), so
// sharing never changes its env or restarts it.
func (b *builder) envSpecs() {
	p, st := b.in.Project, b.in.Stack
	pk := st.Pack
	packSrc := "pack " + pack.Ref{Name: pk.Name, Version: pk.Version}.String()
	for i := range b.m.Services {
		s := &b.m.Services[i]
		add := func(layer Layer, src, svc, scheme string, vars map[string]string) {
			for _, k := range sortedKeys(vars) {
				s.EnvSpec = append(s.EnvSpec, EnvVar{Name: k, Value: vars[k], Layer: layer, Source: src, svc: svc, scheme: scheme})
			}
		}
		share := func(src, svc, scheme string, vars map[string]string) {
			for _, k := range sortedKeys(vars) {
				s.ShareEnv = append(s.ShareEnv, EnvVar{Name: k, Value: vars[k], Layer: LayerShare, Source: src, svc: svc, scheme: scheme})
			}
		}
		agent := b.agents[s.ID]
		consumer := s.Role == RoleApp || s.Role == RoleWorker || b.tools[s.ID] != nil || agent != nil
		worktreeEnv := consumer || s.Role == RoleTool

		if consumer || s.Role == RoleDeps {
			add(LayerPack, packSrc+" [env]", s.ID, s.scheme, pk.Env)
		}
		if s.Role == RoleDeps {
			add(LayerPack, packSrc+" [deps] frozen_env", s.ID, s.scheme, pk.Deps.FrozenEnv)
		}
		if consumer {
			for _, slot := range config.Slots {
				ch := st.Options[slot]
				if ch == nil || ch.Service == s.ID {
					continue
				}
				add(LayerOption, optionSource(ch.Option)+" [gives]", ch.Service, pk.DBScheme[ch.Option.Name], ch.Option.Gives)
			}
		}
		if s.Role == RoleDeps {
			for _, slot := range config.Slots {
				if ch := st.Options[slot]; ch != nil {
					add(LayerOption, optionSource(ch.Option)+" [deps_env]", ch.Service, pk.DBScheme[ch.Option.Name], ch.Option.DepsEnv)
				}
			}
		}
		if o := b.opts[s.ID]; o != nil {
			add(LayerOption, optionSource(o)+" [env]", s.ID, s.scheme, o.Env)
		}
		if t := b.tools[s.ID]; t != nil {
			add(LayerOption, "tool "+t.Name+" [env]", s.ID, s.scheme, t.Env)
		}
		if agent != nil {
			for _, slot := range config.Slots {
				if ch := st.Options[slot]; ch != nil {
					add(LayerOption, optionSource(ch.Option)+" [agent_gives]", ch.Service, pk.DBScheme[ch.Option.Name], ch.Option.AgentGives)
				}
			}
			add(LayerOption, "agent "+agent.Ref.String()+" [env]", s.ID, s.scheme, agent.Agent.Env)
		}
		if s.Role != RoleDeps {
			add(LayerProject, "project.toml [env]", s.ID, s.scheme, p.Env)
		}
		if n := p.Tools.Nvim; n != nil && b.tools[s.ID] != nil && b.tools[s.ID].Name == "nvim" {
			add(LayerProject, "tools.nvim", s.ID, s.scheme, nonEmpty(map[string]string{
				"NVIM_CONFIG_REPO": n.ConfigRepo, "NVIM_CONFIG_REF": n.ConfigRef,
			}))
		}
		if agent != nil {
			add(LayerProject, "project.toml [agents."+agent.Name+"] env", s.ID, s.scheme, agent.Env)
		}
		add(LayerService, "project.toml [services."+s.ID+".env]", s.ID, s.scheme, p.Services[s.ID].Env)
		if worktreeEnv {
			add(LayerWorktree, "worktree", s.ID, s.scheme, map[string]string{
				"WORKTREE_ID": "{worktree.id}", "WORKTREE_SLUG": "{worktree.slug}", "WORKTREE_HOST": "{worktree.host}",
			})
		}
		if agent != nil {
			add(LayerWorktree, "agent "+agent.Name, s.ID, s.scheme, map[string]string{
				"RAILSWITCH_AGENT":       agent.Name,
				"RAILSWITCH_STATUS_FILE": statusMount + "/" + agent.Name + ".json",
			})
		}
		if s.Role != RoleDeps {
			for _, port := range s.Publish {
				s.EnvSpec = append(s.EnvSpec, EnvVar{Name: port.Env, Value: "{" + port.Env + "}", Layer: LayerWorktree, Source: "worktree port " + port.Name, svc: s.ID, scheme: s.scheme})
			}
		}
		b.gitWiring(s)

		if consumer {
			share(packSrc+" [share] env", s.ID, s.scheme, pk.Share.Env)
			for _, slot := range config.Slots {
				if ch := st.Options[slot]; ch != nil && ch.Service != s.ID {
					share(optionSource(ch.Option)+" [share] gives", ch.Service, pk.DBScheme[ch.Option.Name], ch.Option.Share.Gives)
				}
			}
		}
		if s.Role != RoleDeps {
			share("project.toml [share] env", s.ID, s.scheme, p.Share.Env)
		}
	}
}

// gitWiring gives s the git setup of every secret with git_host that s
// receives (D33): a credential helper and an ssh→HTTPS rewrite through
// GIT_CONFIG_* env, so nothing is written into the shared .git/config, and
// the host's commit identity.
func (b *builder) gitWiring(s *Service) {
	p := b.in.Project
	var pairs [][2]string
	var srcs []string
	for _, name := range sortedKeys(p.Secrets) {
		sec := p.Secrets[name]
		if sec.GitHost == "" || !slices.Contains(sec.Services, s.ID) {
			continue
		}
		user := sec.GitUser
		if user == "" {
			user = "x-access-token"
		}
		read := `"$(cat /run/secrets/` + name + `)"`
		if sec.Env != "" {
			read = `"$` + sec.Env + `"`
		}
		helper := `!f() { test "$1" = get && printf 'username=` + user + `\npassword=%s\n' ` + read + `; }; f`
		pairs = append(pairs,
			[2]string{"credential.https://" + sec.GitHost + ".helper", helper},
			[2]string{"url.https://" + sec.GitHost + "/.insteadOf", "git@" + sec.GitHost + ":"})
		srcs = append(srcs, "project.toml [secrets."+name+"]")
	}
	if len(pairs) == 0 {
		return
	}
	src := strings.Join(srcs, ", ")
	env := func(name, value, src string) {
		s.EnvSpec = append(s.EnvSpec, EnvVar{Name: name, Value: literal(value), Layer: LayerSecret, Source: src, svc: s.ID, scheme: s.scheme})
	}
	env("GIT_CONFIG_COUNT", strconv.Itoa(len(pairs)), src)
	for i, kv := range pairs {
		env("GIT_CONFIG_KEY_"+strconv.Itoa(i), kv[0], src)
		env("GIT_CONFIG_VALUE_"+strconv.Itoa(i), kv[1], src)
	}
	h := b.in.Host
	for _, who := range []string{"AUTHOR", "COMMITTER"} {
		if h.GitName != "" {
			env("GIT_"+who+"_NAME", h.GitName, "host git config user.name")
		}
		if h.GitEmail != "" {
			env("GIT_"+who+"_EMAIL", h.GitEmail, "host git config user.email")
		}
	}
}

var (
	gitHostRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)
	gitUserRe = regexp.MustCompile(`^[A-Za-z0-9._@+-]+$`)
)

// checkSecrets refuses a secret delivered as env X to a service that also
// gets X from layers 1–5 or 7: which of podman's --env-file and a type=env secret
// wins is not something to rely on. It also refuses a git_host or git_user
// the credential helper could not carry verbatim.
func (b *builder) checkSecrets() error {
	p := b.in.Project
	var errs []error
	for _, name := range sortedKeys(p.Secrets) {
		sec := p.Secrets[name]
		if sec.GitHost != "" && !gitHostRe.MatchString(sec.GitHost) {
			errs = append(errs, fmt.Errorf("secrets.%s.git_host: %q is not a host name", name, sec.GitHost))
		}
		if sec.GitUser != "" && !gitUserRe.MatchString(sec.GitUser) {
			errs = append(errs, fmt.Errorf("secrets.%s.git_user: %q must match %s", name, sec.GitUser, gitUserRe))
		}
		if sec.Env == "" {
			continue
		}
		for _, id := range sec.Services {
			s := b.m.service(id)
			for _, e := range slices.Concat(s.EnvSpec, s.ShareEnv) {
				if e.Name == sec.Env && (e.Layer <= LayerWorktree || e.Layer == LayerShare) {
					errs = append(errs, fmt.Errorf("secrets.%s: delivered to %s as env %s, but %s already gets %s from %s; rename one of them",
						name, id, sec.Env, id, sec.Env, e.Source))
					break
				}
			}
		}
	}
	return errors.Join(errs...)
}

// envValues sets Values["env.<NAME>"] from env layers 1–3 (pack [env],
// options' [gives], project [env]; highest wins). A value that needs a
// generated value or another env.* value is left unset, so Values never
// carries a generated value.
func (m *Model) envValues(in *Instance) {
	p, st := m.Inputs.Project, m.Inputs.Stack
	pk := st.Pack
	app := m.Services[0].ID
	type cand struct{ value, svc, scheme string }
	cands := map[string]cand{}
	for k, v := range pk.Env {
		cands[k] = cand{v, app, ""}
	}
	for _, slot := range config.Slots {
		if ch := st.Options[slot]; ch != nil {
			for k, v := range ch.Option.Gives {
				cands[k] = cand{v, ch.Service, pk.DBScheme[ch.Option.Name]}
			}
		}
	}
	for k, v := range p.Env {
		cands[k] = cand{v, app, ""}
	}
	out := map[string]string{}
	for k, c := range cands {
		if v, err := pack.Expand(c.value, scope{values: in.Values, svc: c.svc, scheme: c.scheme}); err == nil {
			out["env."+k] = v
		}
	}
	for k, v := range out {
		in.Values[k] = v
	}
}

// expandEnv resolves every service's effective env: per name the highest
// layer, expanded with Values, {service}, {scheme} and {generated.*}. A
// value that expands to an unset {env.X} is dropped.
func (m *Model) expandEnv(in *Instance) error {
	gen := m.Inputs.Generated
	if gen == nil {
		gen = map[string]string{}
	}
	file := filepath.Join(m.Inputs.Workspace, "mise.local.toml")
	missing := func(name string) error {
		return fmt.Errorf("%s is not set (or empty) in %s (railswitch project new generates it; restore it under [env])", name, file)
	}
	var errs []error
	for _, slot := range config.Slots {
		ch := m.Inputs.Stack.Options[slot]
		if ch == nil {
			continue
		}
		for _, g := range ch.Option.Generated {
			if gen[g] == "" {
				errs = append(errs, fmt.Errorf("%s: %w", ch.Option.Name, missing(g)))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}

	in.Env = map[string][]EnvVar{}
	for _, s := range m.Services {
		eff := map[string]EnvVar{}
		for _, e := range s.EnvSpec { // lowest layer first: the last one wins
			eff[e.Name] = e
		}
		if in.Worktree.Share != nil { // layer 7 (D46)
			for _, e := range s.ShareEnv {
				eff[e.Name] = e
			}
		}
		var out []EnvVar
		for _, name := range sortedKeys(eff) {
			e := eff[name]
			v, err := pack.Expand(e.Value, scope{values: in.Values, svc: e.svc, scheme: e.scheme, gen: gen})
			var unknown *pack.UnknownPlaceholderError
			switch {
			case errors.Is(err, pack.ErrUnset):
				continue
			case errors.As(err, &unknown) && strings.HasPrefix(unknown.Name, "generated."):
				return fmt.Errorf("%s: env %s: %w", s.ID, name, missing(strings.TrimPrefix(unknown.Name, "generated.")))
			case err != nil:
				return fmt.Errorf("%s: env %s (%s): %w", s.ID, name, e.Source, err)
			}
			if strings.ContainsAny(v, "\r\n\x00") {
				return fmt.Errorf("%s: env %s (%s): value contains a newline or NUL", s.ID, name, e.Source)
			}
			e.Value = v
			out = append(out, e)
		}
		in.Env[s.ID] = out
	}
	return nil
}

// nonEmpty is vars without its empty values.
func nonEmpty(vars map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range vars {
		if v != "" {
			out[k] = v
		}
	}
	return out
}
