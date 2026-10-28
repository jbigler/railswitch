package lifecycle

import (
	"context"
	"errors"
	"os"

	"github.com/jbigler/railswitch/internal/render"
)

// Deps runs argv in a transient container of worktree slug's deps service
// (D35): its image, mounts and firewall profile, with its env minus the
// pack's [deps] frozen_env names, so argv may change the lockfiles. The env
// goes through a temporary 0600 file, removed afterwards. Like exec it has
// no deadline: it is user-driven, may prompt, and a deadline would put it in
// a background process group that the terminal stops (SIGTTIN/SIGTTOU).
func (l *L) Deps(ctx context.Context, slug string, argv []string) error {
	if len(argv) == 0 {
		return errors.New("deps: no command")
	}
	m, in, err := l.load(ctx, slug)
	if err != nil {
		return err
	}
	s, err := service(m, "deps")
	if err != nil {
		return err
	}
	frozen := m.Inputs.Stack.Pack.Deps.FrozenEnv
	var env []render.EnvVar
	for _, v := range in.Env[s.ID] {
		if _, ok := frozen[v.Name]; !ok {
			env = append(env, v)
		}
	}
	f, err := os.CreateTemp("", "railswitch-deps-*.env") // 0600
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(render.EnvFile(env)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	args, err := l.transient(m, in, s, f.Name(), runOpts{}, argv)
	if err != nil {
		return err
	}
	return l.inContainer(ctx, args)
}
