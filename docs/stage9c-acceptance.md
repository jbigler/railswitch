# Stage 9c acceptance: the app's firewall

Host run on 2026-10-29 with the branch build installed. Mastodon and Maybe
have an agent (so their default is `enforce`); Plausible and Metabase
have none (`log`, now with the `app` profile).

| Check | Result |
|---|---|
| Mastodon render: the app, worker, deps and data units change (`firewall=app`, `mode=enforce`, the lists' digest) | PASS |
| Mastodon restart | FAILED at first: `hook.sh` refused the new profile (`unknown firewall profile 'app'`) and the app didn't start (fail closed). Fixed: the hook takes `app`; `TestHookAppProfile` fails without the fix |
| Mastodon under enforce, empty `app` list | Vite failed: corepack fetches Yarn from `repo.yarnpkg.com` at each start (the Cloudflare address log mode had recorded). Allowed through a Settings `firewall.allow.add` edit |
| That edit's `--restart` | Restarted nothing: a list change didn't change any unit. Fixed: each firewalled unit carries a digest of its lists (`firewall.lists=`), so the preview lists the running worktrees and `up` recreates them |
| Mastodon after the restart: `/`, `/about`, `/explore`, `/api/v1/instance`, `/auth/sign_in` all 200; `fw show`: rails and worker `profiles=app mode=enforce`, nothing rejected | PASS |
