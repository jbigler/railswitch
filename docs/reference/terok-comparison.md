# terok comparison: handover

Status on 2026-11-06: every item is built or set aside, each with its PR
and decision row:

| # | Item | Outcome |
|---|---|---|
| 1 | Address floor | Built: #140, D179 |
| 2 | Allow sets filled by DNS | Built: #144 (stacked on #140), D182 |
| 3 | Temporary allows | Built: #140, D179 (`fw allow --for`) |
| 4 | Check the live ruleset | Built: #140, D179 |
| 5 | Stand-in tokens | Set aside (below) |
| 6 | Git gate | Set aside (below) |
| 7 | `railswitch panic` | Built: #143, D181 |
| 8 | Empty `/etc/hosts` | Built: #143, D181 (`--hosts-file=none`, not `--no-hosts`) |

Also from this work: `meta:google` (#142, D180), Google's service ranges
without Google Cloud's.

**Why 5 is set aside.** The firewall already keeps an agent from sending
the token anywhere but GitHub and its own API. A broker would stop it
copying the token out for use after the session, but while the agent
runs it can still do whatever the token allows, through the broker,
unless the broker checks every request against a policy, which is a large
project. A fine-grained GitHub token scoped to the one repository, with
only the permissions the agent needs and a short expiry, gives most of
the benefit for no code. Revisit if agents need tokens with wider reach.

**Why 6 is set aside.** railswitch worktrees share the host repository,
and you already review and push from the host; the note's own overlap
question stands. With 5 set aside, a gate's main benefit (scoped pushes)
isn't there either.

## What was compared

[terok](https://github.com/terok-ai/terok) runs AI coding agents in rootless
podman. It is written in Python, labelled alpha, Apache-2.0, and split across
six packages: terok, terok-executor, terok-sandbox, terok-shield,
terok-clearance and terok-util. All 11 repos in the terok-ai org were read.
`terok-survey.md`, next to this file, lists each feature, how it works, and a
link to the file or doc it comes from. Its §4 maps terok onto railswitch
without reading railswitch's code, so it is inference. The table below
replaces it.

Many terok doc pages say an AI agent wrote them. Where a mechanism mattered,
the survey checked it against source.

## History

On 2026-10-19, in the `filial` project (the predecessor setup), we looked at
terok-shield alone. That review produced railswitch's firewall design: rules
applied from outside the container by an OCI hook, a container that won't
start when the hook fails, firewall profiles, and log-first rollout. The
`internal/firewall/assets/hook.sh` header still names the sudo hole the hook
closed. Until this note, nothing in this repo mentioned terok.

## Already covered by railswitch

| terok feature | railswitch equivalent |
|---|---|
| Firewall rules applied by an OCI hook, container fails to start on error | `internal/firewall/assets/hook.sh`, `TestHookFailsClosed` |
| Firewall profiles | `app`, `deps`, `agent-<name>`, `none` (D119, `docs/firewall.md`) |
| DOWN posture (allow everything, log what would be blocked) | `[firewall] mode = "log"` |
| Live allow | `railswitch fw allow` (`firewall.sh`, `nft add element … allow4`) |
| Harvest → promote; clearance prompts | `blocked4`/`blocked6` sets, `fw show`, the Firewall card's Allow button |
| Repo hooks never run on the host | `core.hooksPath=/dev/null` and `core.fsmonitor=false` on every host git call; `internal/gitshadow`; `internal/gitguard` |
| Agent status file read as untrusted | `internal/agentstatus` (D58) |
| Host-only command queue | `internal/agentqueue` (D159) |
| Untrusted-filesystem I/O, terminal escape stripping | `safefile.Read`, `safefile.Printable` |
| Token on the web UI | dashboard token cookie plus `Origin` check |

## Candidates

Ranked by value for the effort. Each item adopted gets a D-row in
`docs/superpowers/plans/2026-10-25-railswitch-roadmap.md`. The last row is
D175.

### 1. An address floor no allow entry can open (small)

**Gap, read in code:** `hook.sh` lines 100–143 add to `allow4` any IPv4
literal or CIDR in an allow list, and every address `getent ahostsv4`
returns for a name. So `0.0.0.0/0`, `169.254.169.254` (cloud metadata), or
a LAN name that resolves to `192.168.x.x` all open. `firewall_test.go:181`
writes `10.0.0.0/8` into `agent.allow` without complaint.

**terok:** tier t00, a hard-deny floor ahead of every allow: link-local,
IMDS (169.254/16 and the AWS IPv6 IMDS), the host's loopback and gateway.
Tier t20 rejects RFC 1918 and RFC 4193 private ranges. Links are in
survey §2.2, rows "Tiered policy" and "Postures".

**Shape:** in the `output` chain, after `ip daddr @local4 accept` and before
`@allow4`, reject `169.254.0.0/16`, `10.0.0.0/8`, `172.16.0.0/12`,
`192.168.0.0/16`, `100.64.0.0/10` and `0.0.0.0/8`. This order still lets
the container's own networks, which `local4` holds, through. Also refuse
those literals when `config.AddFirewallAllow` adds them, and in
`firewall.sh`'s live allow, so the user sees an error instead of a silent
drop.

**Open questions:**
- Does Tailscale (`internal/tailscale`, `100.64/10`) need an exception?
- Does anything legitimately allow a LAN host today? An opt-in escape
  would need its own lint.

### 2. Allow sets filled by DNS (medium; needs a spec)

**Gap:** `docs/firewall.md` "Known limits" names two:
- The worktree resolver forwards any name upstream, which is a channel for
  leaking data through DNS.
- Addresses are resolved once, when the container starts, so a CDN whose
  addresses rotate needs a restart.

**terok:** a dnsmasq per container, run with `nftset=` directives and
`cache-size=0`, writes each resolved address into the allow set before the
reply reaches the workload. Names off the list get no answer. Wildcard
names work. Elements carry timeouts. The survey's §2.2 row "DNS tiers" links
the design.

**Costs:**
- dnsmasq becomes a host tool for `doctor` to check.
- The container's `resolv.conf` has to point at its dnsmasq.
- terok documents AppArmor work for dnsmasq.
- It interacts with aardvark-dns and the dev network's DNS, which other
  containers resolve through.

### 3. Temporary allows the kernel expires (small)

**Gap:** an `fw allow` lasts until the container restarts.

**terok:** break-glass overrides take a mandatory reason and an optional
expiry, and bypass windows are timed. Both are nft set elements with a
per-element `timeout`, so the kernel expires them and a crashed host
process can't stretch them.

**Shape:** add `fw allow --for <duration>` and use `nft add element …
{ <ip> timeout 30m }`. **Risk:** `allow4` is declared `flags interval;
auto-merge`. Per-element timeouts need `timeout` in its flags, and
interval+timeout depends on the kernel and nft version. Test on podman
5.3's minimum platform, or put timed allows in a separate set.

### 4. Check the live ruleset after applying it (small)

**Gap:** the hook trusts `nft -f` returning 0.

**terok:** `verify_up`, `verify_down` and `verify_quarantine` check the
ruleset's invariants, and a failure raises.

**Shape:** after line 215 of `hook.sh`, run `nsenter … nft list table inet
$TABLE` and confirm:
- the output chain ends in the expected verdict;
- the floor rules from item 1 are present;
- `allow4` holds what was asked for.

On a mismatch, `die`, which fails the container closed.

### 5. Stand-in tokens behind a host proxy (large; needs a spec)

**Gap:** an agent gets the real `github_token` as `GH_TOKEN`
(`docs/guide/agents.md` "No host credentials" and "GitHub CLI"), and
`github.com` is on its allow list. An agent tricked by injected text could
read the token and send it out to a repository it controls.

**terok:** the container holds only a stand-in ("phantom") token per task
and provider. A broker per container checks the stand-in and injects the
real header. Firewall tier t20 blocks the provider hosts directly, so only
the broker can reach them. Tokens can be revoked live. Each use is written
to a JSONL audit log. For SSH, a signer speaks the SSH-agent protocol, so
no key file enters the container. Survey §2.3.

**Constraints:**
- D33 (secrets only on stdin) still holds on the host side.
- Git over HTTPS goes through a credential helper set in `GIT_CONFIG_*`
  (`internal/render/env.go:246`), which would point at the broker.
- `gh` honours `http_unix_socket`.

### 6. Git gate (large; needs a spec)

**terok:**
- Agents push to a bare mirror on the host, over smart HTTP with a token
  per task.
- A `post-receive` hook saves the old tip of every force-push or delete as
  `refs/terok/backup/...`.
- `transfer.hideRefs` hides terok's own refs from containers.
- Sync from upstream only creates or fast-forwards branches.
- A person promotes to upstream.

terok's docs say the gate means little without the firewall.

**Overlap:** railswitch worktrees share the host repo's object store
(`gitshadow`). Pushes to a real remote with a pending check already exist
(`gh stack`). Decide whether a gate fits worktrees, or whether item 5's
token scoping (push only to the gate) gets most of the benefit.

### 7. `railswitch panic` (small)

**terok:** `terok panic` runs these steps in parallel, each best-effort with
a deadline:
- SIGKILL every managed container, chosen by trusted annotation;
- quarantine the network;
- lock the vault.

The containers stay on disk for forensics.

**Shape:** stop every agent container across projects by exact name (the
registry knows them) and switch each firewall table to drop-all. Follow
the AGENTS.md rule: never `--filter`. Needs `--json`, and a synopsis in
`help.go`.

### 8. Empty `/etc/hosts` (trivial, unverified)

**terok:** `--hosts-file none` keeps host-private names out of containers.

**Unverified:** whether Quadlet at podman 5.3 exposes this as a key or
needs `PodmanArgs=`. Containers reach each other by DNS on the dev
network, so check that nothing reads `/etc/hosts` first.

## Not worth taking

- The Textual TUI and web TUI: the dashboard covers them.
- The YAML config stack: TOML plus `env.commit` already does this.
- The krun microVM runtime.
- Seccomp grants for ThreadSanitizer and perf.
- The YAML agent and provider roster: packs cover it.
- The prompt-acknowledgement marker files: railswitch types into tmux.
- Desktop notifications over D-Bus: the dashboard's Allow button covers
  them.

## Unresolved in terok's docs

terok's README promises a "default-deny egress firewall". Its shield guide
calls DOWN (log only) "the default", and a `shield.down_on_task_run`
setting exists. Neither reading changes the list above.

## Verification notes

- Items 1 and 4 can be unit tested the way `TestHookFailsClosed` drives
  `hook.sh` with a fake `nft`.
- Item 3's interval-and-timeout question needs a real kernel. An
  integration test (`//go:build integration`) or a manual run in an
  `unshare -Urn` namespace would settle it.
- Every behaviour change updates `docs/firewall.md` and, for new
  `--json` output, `docs/json.md`.
