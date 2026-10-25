# railswitch Stages 0–2 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Set up a single Go binary, `railswitch`. It turns `project.toml` + `stack.lock` into linted rootless-podman Quadlet units (Stage 1), and runs a fresh Rails project end to end on one machine through its own CLI (Stage 2).

**Architecture:** `config` parses the project files. `pack` resolves the embedded rails pack and its options into a `Stack`. `render` builds one worktree-independent `Model`, from which it writes:
- one Quadlet unit per worktree and service, with every value written in (no `${VAR}`), linted before it is written;
- one env file per worktree and service, holding that service's process env, secrets included.

`lifecycle`, `worktree` and `firewall` drive `podman`, `systemctl` and `git` only through the `sys.Runner` interface. Machine-wide IDs and subnets live in one flock'd registry.

**Tech Stack:** Go (single static binary, `CGO_ENABLED=0`). Libraries: stdlib, plus `github.com/BurntSushi/toml`. Host programs: Quadlet / systemd user units, podman ≥ 5, git, bash (firewall hook only).

**Spec:**
- `handoff/HANDOFF.md`;
- the decisions D1–D43 in `docs/superpowers/plans/2026-10-25-railswitch-roadmap.md` (binding: cite them by number);
- the mock-up values in `docs/reference/design-data.md`;
- the old tool: `https://github.com/jbigler/rails-dev-podman` at commit `fd7336b` (`AGENTS.md` and `.container-config/`).

## Global Constraints

- **Names:** binary/CLI `railswitch`; module `github.com/jbigler/railswitch`.
- **Paths** (honour `XDG_CONFIG_HOME`/`XDG_DATA_HOME`):
  - config dir `~/.config/railswitch/`;
  - data dir `~/.local/share/railswitch/`;
  - Quadlet output `~/.config/containers/systemd/`.
- **Calling commands:** argv only, never a shell; every call has a timeout (`sys.Cmd.Timeout`, default 30 s) or is an explicit interactive/follow call (`sys.NoTimeout`: `logs -f`, `exec`); all calls go through `sys.Runner`.
- **No podman Go bindings.**
- **Secrets (D33):** a secret value only ever travels on stdin: never on argv, in a file railswitch writes, an env file, a log or a job output. No host SSH agent, `~/.ssh` or host gitconfig is mounted into any container.
- **Dependencies:** stdlib and `github.com/BurntSushi/toml`. The no-echo prompt for `secret set` uses stdlib `syscall` `TCGETS`/`TCSETS` ioctls on linux (clear `ECHO`, restore afterwards).
- **Container hardening (D34):** every rendered container has `NoNewPrivileges=true`, `Memory=` and `PidsLimit=`. App, deps, worker and tool containers also get `DropCapability=all`, except the browser (it keeps `AddCapability=NET_ADMIN`/`NET_RAW` and gets no `DropCapability`).
- **Schema versions (D39):** `project.toml`, `stack.lock`, `registry.toml` and the machine `config.toml` carry `schema = 1`. A file with a higher schema is refused ("written by a newer railswitch"), never misread.
- **Machine-readable output (D40):** every read command (`env`, `status`, `wt ls`, `secret ls`, `containerfile list`, `doctor`) accepts the global `--json` flag and prints one JSON document with stable field names, documented in `docs/json.md`.
- **Unit tests** need no podman, systemd, gh or network. `git` and `bash` are allowed.
- **Integration tests** carry `//go:build integration`.
- **Before every commit:** `make check` (`gofmt -l` is empty, `go vet ./...`, `staticcheck ./...`, `go test ./...`).
- **Publishing:** every published port is `127.0.0.1:<host>:<ctr>`; nothing ever binds a non-loopback address.
- **Exact names:** objects are removed by exact name, never by a `--filter` or a regex.
- **Prune:** never `git worktree prune`.
- **Hostile input:** files inside a worktree (`.ruby-version`, `Gemfile.lock`, `mise.local.toml` *inside* a worktree…) are read only through `safefile.Read`: regular files only, at most 1 MiB.
- **Pinned values:**

  | What | Value |
  |---|---|
  | Subnet pool | `10.214.0.0/16`, one `/24` per project |
  | Gateway | `.1` |
  | Traefik | `.2` |
  | Dynamic range | `10.214.<n>.128/25` |
  | Dev network | MTU 1400 (`Options=mtu=1400`) |
  | DB port | 55432 + ID |
  | Ruby debug | 33000 + ID |
  | Neovim | 17000 + ID |
  | Playwright | 18888 + ID |
  | App (share) | 13000 + ID |
  | Storage (share) | 19000 + ID |
  | Worktree slug | 40 chars max |
  | `LogOpt=max-size` | `20m` |

## Review Focus

1. **A hostile or odd version file:** a symlinked `.ruby-version`, a FIFO, 2 MiB of junk, or `3.3.6; rm -rf ~`. Expect that source to be skipped with a warning, the chain falling through to the next source or the fallback, and the value never reaching a tag or build arg. *Pinned in Task 6.*
2. **Slugs that prefix each other (`api` and `api-v2`):** removing or downing `api` must leave every `api-v2` container, volume, unit and env dir alone. *Pinned in Tasks 14, 17 and 18.*
3. **A missing secret value** (no `mise.local.toml`, or a name absent from it): render must stop, naming the secret and the file. It must never write `DATABASE_URL=postgres://postgres:@db/...`. *Pinned in Task 8.*
4. **Re-running after a partial failure:** `git worktree add` fails (branch checked out elsewhere), or an image build fails mid-`up`. Expect no registry entry, no env dir, no half-written unit; a second run succeeds. *Pinned in Tasks 11, 17 and 18.*
5. **User strings with `%`, `$`, spaces or newlines** in `[env]`, `[services.*.env]` or a link:
   - In unit files, user-supplied text has `%` → `%%` and `$` → `$$`, and a value with spaces is quoted.
   - Env files hold values verbatim.
   - A value containing a newline is a validation error.

   *Pinned in Tasks 3 and 9.*

---

## File Structure

```
cmd/railswitch/main.go            # os.Exit(cli.Main(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
internal/cli/                     # flag-based dispatch; one file per command group
internal/sys/runner.go            # Runner, Cmd, Result, Exec (os/exec impl)
internal/sys/systest/fake.go      # Fake runner for tests
internal/safefile/safefile.go     # hostile-file reads
internal/naming/naming.go         # Sanitize, object/unit names (D6, D7)
internal/placeholder/placeholder.go # placeholder grammar shared by config and pack (Task 3)
internal/config/{project,lock,generated,hsts,edit}.go
internal/pack/{ref,format,load,hash,expand,resolve}.go
internal/pack/builtin/            # embedded: packs/rails, options/{postgres,redis,rustfs,playwright,sidekiq}, tools/nvim
internal/version/{chain,parsers}.go
internal/machine/{dirs,registry,proxy,config}.go
internal/render/{model,env,images,quadlet,lint,diff,apply}.go + testdata/
internal/podman/{podman,systemd}.go
internal/firewall/{descriptor.go, assets/{hook.sh,firewall.sh}}
internal/worktree/{add,remove,resolve}.go
internal/workspace/workspace.go   # workspace discovery + the single Render path (Task 12)
internal/lifecycle/{up,stop,down,status,logs,exec,build,preflight}.go
internal/doctor/doctor.go
testdata/legacy/quadlet/          # old units at fd7336b, for the equivalence review only
testdata/fixtures/filial/         # project.toml, mise.local.toml, db-dumps/, master/ and feat/ worktree files
testdata/fixtures/minimal/        # rails with database/cache/browser none, no tools (Task 9)
docs/json.md                      # --json schemas (D40), created in Task 12
```

---

## Stage 0 — Set up

### Task 0: Bootstrap the module

**Files:**
- Create:
  - `.gitignore` (`/railswitch`, `/dist/`)
  - `go.mod`, `Makefile`
  - `cmd/railswitch/main.go`, `internal/cli/cli.go`, `internal/cli/cli_test.go`
  - `AGENTS.md`
  - `.github/workflows/ci.yml`, `.github/workflows/release.yml` (D41: railswitch's own CI; railswitch never generates or touches a project's CI)

**Interfaces:**
- Produces:
  - `func cli.Main(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int`
  - `var buildinfo.Version = "dev"` (package `internal/buildinfo`) (set with `-ldflags "-X github.com/jbigler/railswitch/internal/buildinfo.Version=…"`)

- [ ] **Step 1: Prerequisites (user action).** Go is not installed on this host: `sudo pacman -S go`. Then `go install honnef.co/go/tools/cmd/staticcheck@latest`.
  - Check: `go version` and `staticcheck -version` both print a version.
- [ ] **Step 2:** In the workspace root run `git init` and `go mod init github.com/jbigler/railswitch`. Keep `handoff/` in the repo; it is the spec.
- [ ] **Step 3: Write the failing tests.**
  - `TestVersion`: `Main(ctx, []string{"version"}, …)` returns 0 and stdout is `railswitch dev\n`.
  - `TestVersionJSON`: `Main(ctx, []string{"--json", "version"}, …)` prints `{"version":"dev"}\n`.
  - `TestUnknownCommand`: `Main(ctx, []string{"nope"}, …)` returns 2 and stderr contains `unknown command "nope"`.
- [ ] **Step 4:** Run `go test ./internal/cli/`. Expected: FAIL (undefined `Main`).
- [ ] **Step 5: Implement `cli.Main`.**
  - A `map[string]command` dispatch, where `command` is `func(ctx, *env, []string) int` and `env` carries stdin/stdout/stderr.
  - Global flags parsed before the command: `-C <dir>` (start dir for workspace discovery), `-w <slug>` (worktree), and `--json` (D40; carried in `env`).
- [ ] **Step 6: Write the Makefile and workflows.**
  - `make check` runs `test -z "$(gofmt -l .)"`, `go vet ./...`, `staticcheck ./...` and `go test ./...`.
  - `make build` runs `CGO_ENABLED=0 go build -trimpath -ldflags "-X github.com/jbigler/railswitch/internal/buildinfo.Version=$(VERSION)" -o railswitch ./cmd/railswitch`.
  - `ci.yml`, on push and pull request: job `check`: `make check` on `ubuntu-latest` (Go version from `go.mod`). This is its only job here; Task 13 adds the `quadlet` job.
  - `release.yml`, on tags `v*`:
    - `make build` for `linux/amd64` and `linux/arm64` with `VERSION=${tag}`;
    - `sha256sum` into `SHA256SUMS`;
    - `gh release create` with the binaries and checksums.
  - The full end-to-end test (Task 21) needs a systemd user session and stays a manual run.
- [ ] **Step 7: Write `AGENTS.md`** (≤ 60 lines): the Global Constraints above, the package map, "Render lints are the gotchas from HANDOFF §7", and "Every behaviour change updates the relevant doc in `docs/`".
- [ ] **Step 8:** Run `make check`. Expected: PASS.
- [ ] **Step 9:** `git add -A && git commit -m "chore: bootstrap railswitch module"`.

---

## Stage 1 — Engine and lint (no UI)

### Task 1: Command runner

**Files:**
- Create: `internal/sys/runner.go`, `internal/sys/runner_test.go`, `internal/sys/systest/fake.go`, `internal/sys/systest/fake_test.go`

**Interfaces:**
- Produces:

  ```go
  type Cmd struct {
      Name    string
      Args    []string
      Dir     string
      Env     []string        // nil = inherit
      Stdin   io.Reader
      Stdout  io.Writer       // nil = captured into Result
      Stderr  io.Writer
      Timeout time.Duration   // 0 = DefaultTimeout (30s); NoTimeout = no deadline (interactive/follow calls only)
  }
  type Result struct { Stdout, Stderr []byte; ExitCode int }
  type Runner interface { Run(ctx context.Context, c Cmd) (Result, error) }
  type Exec struct{}         // real implementation
  var ErrExit = errors.New("non-zero exit")   // wrapped together with the exit code and stderr tail
  const DefaultTimeout = 30 * time.Second
  const NoTimeout time.Duration = -1          // logs -f and exec; the call ends only with its ctx
  // systest:
  type Fake struct {
      Passthrough []string    // binary names run for real (e.g. "git")
      Calls       [][]string  // argv of each call, Name first
  }
  func (f *Fake) On(prefix []string, r Result, err error)   // longest matching prefix wins; unmatched → Result{} and nil
  ```

- [ ] **Step 1: Write failing tests.**
  - `TestExecArgvLiteral`: running `printf %s 'a;b $(x)'` gives stdout `a;b $(x)` exactly.
  - `TestExecExitCode`: `false` gives `errors.Is(err, ErrExit)` and `ExitCode == 1`.
  - `TestExecTimeout`: `sleep 5` with a 100 ms timeout returns within 1 s and `errors.Is(err, context.DeadlineExceeded)`.
  - `TestDeadlineFor` (in-package, on the unexported `func deadlineFor(c Cmd) (time.Duration, bool)`): `Timeout 0` → `30s, true`; `Timeout 5s` → `5s, true`; `Timeout NoTimeout` → `0, false` (no deadline is set; only the caller's ctx can end the call).
  - `TestExecNoTimeoutCancel`: `sleep 5` with `Timeout: NoTimeout` and a parent ctx cancelled after 100 ms returns within 1 s with `errors.Is(err, context.Canceled)`.
  - `TestFakeRecordsAndScripts`: the most specific `On` wins, and `Calls` records the argv.
  - `TestFakePassthrough`: `git --version` runs for real when `"git"` is in `Passthrough`.
- [ ] **Step 2:** Run `go test ./internal/sys/...`. Expected: FAIL.
- [ ] **Step 3: Implement** with `exec.CommandContext`. Set `cmd.WaitDelay = 2*time.Second`, and `Setpgid` so a timeout kills the whole group.
- [ ] **Step 4:** Run `go test ./internal/sys/...`. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(sys): argv-only command runner with fake"`.

### Task 2: Hostile-file reads

**Files:**
- Create: `internal/safefile/safefile.go`, `internal/safefile/safefile_test.go`

**Interfaces:**
- Produces:
  - `const Limit = 1 << 20`
  - `var ErrRefused = errors.New("refused")`
  - `func Read(path string, limit int64) ([]byte, error)`

- [ ] **Step 1: Write failing tests**, one per case:
  - a regular file is returned;
  - a symlink to a regular file gives `ErrRefused`;
  - a FIFO gives `ErrRefused` within 100 ms, with no writer present;
  - a directory gives `ErrRefused`;
  - a file of `Limit+1` bytes gives `ErrRefused`;
  - a missing file gives `errors.Is(err, fs.ErrNotExist)`.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement**, porting `safe_file.rb`:
  1. `Lstat` must be a regular file;
  2. open with `O_RDONLY|O_NONBLOCK|O_NOFOLLOW`;
  3. `Fstat` must be a regular file;
  4. read `limit+1` bytes.
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(safefile): refuse non-regular and oversized files"`.

### Task 3: Naming rules and `project.toml`

**Files:**
- Create:
  - `internal/naming/naming.go` and `naming_test.go`
  - `internal/placeholder/placeholder.go` and `placeholder_test.go`
  - `internal/config/project.go`, `hsts.go`, `project_test.go`
  - `testdata/fixtures/filial/project.toml`: `schema = 1`, then the HANDOFF §3.2 example with its `[secrets] names = […]` line replaced by `[secrets.twilio_auth_token]` (`services = ["rails"]`, `env = "TWILIO_AUTH_TOKEN"`), then `[image]` with `base = "containerfiles/Containerfile.base"`. Task 8 extends it.

**Interfaces:**
- Produces (naming):

  ```go
  func Sanitize(name string) (string, error)     // port of lib.sh sanitize_worktree_name; ErrEmptyName
  func ValidPrefix(p string) error               // D7
  func ValidService(id string) error             // ^[a-z][a-z0-9-]{0,30}$ and not an HSTS-preloaded TLD
  func Container(prefix, slug, svc string) string    // "filial-feat-rails": the container, its .container file and its .service unit (D6)
  func DevNetworkUnit(prefix, slug string) string    // "filial-feat-dev.network"
  func WorktreeVolume(prefix, slug, vol string) string // "filial-feat-db-data-pg16"
  func Shared(prefix, what string) string        // "filial_npm_cache"
  func DevNetwork(prefix, slug string) string    // "filial-feat-dev"
  func ProxyNetwork(prefix string) string        // "filial_proxy"
  ```

- Produces (placeholder; a leaf package that imports nothing of railswitch, so `config` and `pack` both use it):

  ```go
  var Pattern = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_.]*\}`) // "{{" is a literal "{" and never starts a placeholder
  func Names(s string) []string                 // placeholder names in s, in order, without braces; skips "{{" escapes
  var LinkNames = map[string]bool{"worktree.host": true, "worktree.slug": true, "branch": true} // the names a [[links]] url may use (HANDOFF §3.10)
  ```

- Produces (config): the types below, plus:
  - `func Load(path string) (*Project, error)`: strict; an unknown key is an error naming its dotted path;
  - `func (p *Project) Validate() error`: `errors.Join` of every problem;
  - `var Slots = []string{"database", "cache", "storage", "jobs", "mail", "browser"}`.

  ```go
  type Project struct {
      Schema    int                         `toml:"schema"`     // must be 1 (D39)
      From      string                      `toml:"from"`
      Project   Meta                        `toml:"project"`
      Stack     StackRef                    `toml:"stack"`
      Options   map[string]string           `toml:"options"`    // slot → ref, or "none"
      Versions  map[string]string           `toml:"versions"`
      Env       map[string]string           `toml:"env"`
      Services  map[string]ServiceOverride  `toml:"services"`
      Worktrees map[string]WorktreeOverride `toml:"worktrees"`
      Secrets   map[string]Secret           `toml:"secrets"`    // key = secret name; values live in podman secrets (D33)
      Links     []Link                      `toml:"links"`
      Tools     Tools                       `toml:"tools"`
      Firewall  Firewall                    `toml:"firewall"`
      Image     ImageConfig                 `toml:"image"`
  }
  type ImageConfig struct {
      Base   string   `toml:"base"`   // required: "containerfiles/Containerfile.<name>"; railswitch new ejects the pack's default here (D31)
      Addons []string `toml:"addons"` // "containerfiles/Containerfile.<name>", applied in this order on top of base
  }
  type Secret struct {
      Services []string `toml:"services"` // service ids that receive it ("agent" from Stage 4)
      Env      string   `toml:"env"`      // deliver as this env var; empty (default) = file /run/secrets/<name>
      Scope    string   `toml:"scope"`    // "project" (default): podman secret <prefix>_<name>; "machine": railswitch_<name>
      GitHost  string   `toml:"git_host"` // e.g. "github.com": receiving services use it for git over HTTPS (D33)
      GitUser  string   `toml:"git_user"` // default "x-access-token"
  }
  type Meta struct {
      Name, Prefix, Repo string
      BaseBranch         string   `toml:"base_branch"`
      SeedFiles          []string `toml:"seed_files"`
  }
  type StackRef struct{ Pack, App string }
  type ServiceOverride struct {
      Env       map[string]string `toml:"env"`
      Memory    string            `toml:"memory"`     // e.g. "6g"; overrides the pack/option/role default (D34)
      PidsLimit int               `toml:"pids_limit"` // > 0; overrides the default (D34)
      Health    string            `toml:"health"`     // replaces the pack's app health command; "none" disables it (D34)
  }
  type WorktreeOverride struct{ Versions map[string]string }
  type Link struct{ Label, URL string }
  type Tools struct{ Nvim bool }                   // opt-in; default false
  type Firewall struct {
      Mode  string              // "log" (default) | "enforce" | "off"
      Allow map[string][]string // profile → extra hosts
  }
  ```

- [ ] **Step 1: Write the naming tests.** `Sanitize`:
  - `"Feature/Foo Bar"` → `"feature-foo-bar"`;
  - `"--x--"` → `"x"`;
  - `"a\nb"` → `"a-b"`;
  - 50 × `"ab-"` → at most 40 chars, no trailing `-`;
  - `"!!!"` → `ErrEmptyName`.

  `ValidService`: `app`, `dev`, `page`, `new` are rejected with a message containing `HSTS`; `rails` and `db` pass.

  `placeholder.Names`: `"{worktree.host}/x/{branch}"` → `["worktree.host","branch"]`; `"{{literal}"` → `[]`; `"/data/rustfs{0..3}"` → `[]`.
- [ ] **Step 2: Write the config tests.**
  - `TestLoadFilial`: loads `testdata/fixtures/filial/project.toml`. Asserts `Schema == 1`, `Options["cache"] == "redis@1"`, `Worktrees["postgres-trial"].Versions["postgres"] == "17"`, `Secrets["twilio_auth_token"].Env == "TWILIO_AUTH_TOKEN"`, and `Image.Base == "containerfiles/Containerfile.base"`; `Validate` returns nil.
  - `TestUnknownKey`: `[optins]` gives an error containing `optins`.
  - `TestValidate` (table-driven), each row expecting an error naming the field:
    - prefix `"Filial"`, `"fi-l"`, `"f"`;
    - unknown slot `worker`;
    - ref `"postgres"` (no `@N`);
    - `[stack] app = "app"`;
    - secret name `db-pass`;
    - an `[env]` value with `\n`;
    - an unknown link placeholder `{worktree.hots}` (link urls are checked with `placeholder.Names` against `placeholder.LinkNames`);
    - `[worktrees."Bad Slug"]`;
    - firewall mode `strict`;
    - `[image] addons = ["Dockerfile"]` and `["../x/Containerfile.y"]` (each must be `containerfiles/Containerfile.<name>`);
    - no `[image] base` (the message contains `railswitch containerfile eject base`);
    - secret name `Twilio-Token` (must match `^[a-z][a-z0-9_]{0,40}$`), `scope = "global"`, `env = "1X"`, `services = []`;
    - no `schema` (the message contains `schema = 1`); `schema = 2` (the message contains `newer railswitch`);
    - `[services.rails] memory = "lots"` (must match `^[0-9]+[kmg]$`); `pids_limit = -5`.
- [ ] **Step 3:** Run `go test ./internal/naming/ ./internal/config/`. Expected: FAIL.
- [ ] **Step 4: Implement.**
  - `hsts.go`: a `map[string]bool` of the Chromium HSTS-preloaded TLDs, taken from `net/http/transport_security_state_static.json` (the entries whose name has no dot). Cite the Chromium commit in a comment.
  - Strict decode: `toml.DecodeFile`, then fail if `md.Undecoded()` is non-empty.
- [ ] **Step 5:** Run the tests. Expected: PASS.
- [ ] **Step 6:** `make check && git commit -am "feat(config): project.toml parsing and validation"`.

### Task 4: Pack and option format, built-ins, content hash

**Files:**
- Create:
  - `internal/pack/ref.go`, `format.go`, `load.go`, `hash.go`, `pack_test.go`
  - `internal/pack/builtin/embed.go` (`//go:embed all:packs all:options all:tools`)
  - `internal/pack/builtin/packs/rails/`: `pack.toml`, `Containerfile.base`, `entrypoints/entrypoint-app.sh`, `entrypoints/entrypoint-deps.sh`, `deps.allow`
  - `internal/pack/builtin/options/postgres/`: `option.toml`, `initdb/restore-dump.sh`, `Containerfile.pg-client` (an app addon, D31)
  - `internal/pack/builtin/options/redis/option.toml`
  - `internal/pack/builtin/options/rustfs/option.toml`
  - `internal/pack/builtin/options/playwright/`: `option.toml`, `Containerfile.playwright`, `entrypoints/entrypoint-playwright.sh`, `init-firewall-playwright.sh`
  - `internal/pack/builtin/options/sidekiq/option.toml`
  - `internal/pack/builtin/tools/nvim/`: `tool.toml`, `Containerfile.nvim`, `entrypoints/entrypoint-nvim.sh`

**Interfaces:**
- Produces:
  - `type Ref struct{ Name string; Version int }`, with `func ParseRef(s string) (Ref, error)` and `String()`;
  - `type Pack`, `type Option`, `type Tool`: Go mirrors of the TOML below, each with `Files fs.FS` (its directory);
  - `type Catalog struct{ Packs map[Ref]*Pack; Options map[Ref]*Option; Tools map[string]*Tool }`;
  - `func LoadBuiltins() (*Catalog, error)`;
  - `func Load(fsys fs.FS) (*Catalog, error)`;
  - `func ContentHash(fsys fs.FS) (string, error)`: D14.
- File format, fixed here; the golden units in Task 9 depend on it. The rails `pack.toml` (values from HANDOFF §3.4 and the mock-up data):

  ```toml
  name = "rails"
  version = 1
  [app]
  service = "rails"
  server = "bin/dev"
  port = 3000
  install = "bundle install && npm ci"            # what deps runs; lockfile-frozen (D35)
  health = "curl -fsS http://localhost:3000/up"   # Rails 7.1+ health route (D34)
  health_start_period = "120s"
  entrypoint = "entrypoints/entrypoint-app.sh"
  deps_entrypoint = "entrypoints/entrypoint-deps.sh"
  [deps]
  frozen_env = { BUNDLE_FROZEN = "true" }         # set on the deps service; `railswitch deps` leaves it out (D35)
  [image]                    # the base image (D31); app, deps and worker run it
  file = "Containerfile.base"
  tag = "ruby{ruby}-node{node}"
  os = "debian"
  args = { RUBY_VERSION = "{ruby}", NODE_VERSION = "{node}" }
  [runtimes.ruby]
  from = [".ruby-version", ".tool-versions:ruby", "Gemfile", "Gemfile.lock"]
  fallback = "3.4.2"
  [runtimes.node]
  from = [".nvmrc", ".node-version", ".tool-versions:nodejs", "package.json"]
  fallback = "22.11.0"
  [runtimes.playwright]
  from = ["Gemfile.lock:playwright-ruby-client"]
  fallback = "1.60.0"
  [db_name]
  from = ["config/database.yml:development.database"]
  fallback = "{project.prefix}_development"
  [env]                      # only what Rails itself reads; app-specific names go in the project's [env]
  RAILS_ENV = "development"
  BINDING = "0.0.0.0"
  [db_scheme]
  postgres = "postgres://"
  mysql = "mysql2://"
  mariadb = "mysql2://"
  [[caches]]                 # shared across worktrees: <prefix>_<name>
  name = "gems_ruby{ruby}"
  mount = "/usr/local/bundle"
  chown = false
  [[caches]]
  name = "npm_cache"
  mount = "/home/appuser/.npm"
  chown = true
  [[worktree_volumes]]       # per worktree: <prefix>-<slug>-<name>
  name = "node-modules"
  mount = "/app/node_modules"
  chown = true
  [[ports]]
  name = "debug"
  env = "RUBY_DEBUG_PORT"
  base = 33000
  container = 38698
  [[ports]]
  name = "app"
  env = "APP_PORT"
  base = 13000
  container = 3000
  [defaults]
  database = "postgres@1"
  cache = "redis@1"
  storage = "none"
  jobs = "none"
  mail = "none"
  browser = "none"           # opt-in: --option browser=playwright@1
  [tasks]
  console = "bin/rails console"
  test = "bin/rails test"
  "test:system" = "bin/rails test:system"
  [[links]]
  label = "Mailer previews"
  url = "{worktree.host}/rails/mailers"
  [[links]]
  label = "Routes"
  url = "{worktree.host}/rails/info/routes"
  ```

  The postgres `option.toml`. The other options follow the same keys, using the values in `docs/reference/design-data.md` §2. Their health and exec lines are copied from the matching `fd7336b` unit (redis `Exec=redis-server --loglevel warning --save '' --appendonly no`; rustfs env and both routes, `s3.` → 9000 and `s3-ui.` → 9001, with the root-redirect middleware).

  ```toml
  name = "postgres"
  version = 1
  slot = "database"
  service = "db"
  works_with = ["*"]
  generated = ["DB_PASSWORD"]   # D17: railswitch generates the value into mise.local.toml; never user-declared
  [pin]
  name = "postgres"
  default = "16"
  [image]
  pull = "docker.io/library/postgres:{postgres}"
  [app_addon]             # D31: extends the app's base image with the client libraries the app needs
  file = "Containerfile.pg-client"
  os = "debian"
  [health]
  cmd = "pg_isready -U postgres"
  interval = "5s"
  timeout = "3s"
  retries = 5
  start_period = "30s"
  [gives]                 # layer 2, to app/worker/tool services
  DATABASE_URL = "{scheme}postgres:{generated.DB_PASSWORD}@{service}/{db}"
  PGHOST = "{service}"   # D29: libpq vars, never given to the db service itself
  PGPORT = "5432"
  PGUSER = "postgres"
  PGPASSWORD = "{generated.DB_PASSWORD}"
  PGDATABASE = "{db}"
  [env]                   # the option's own service
  POSTGRES_PASSWORD = "{generated.DB_PASSWORD}"
  PGTZ = "{env.TZ}"
  [[volumes]]
  name = "db-data-pg{postgres.major}"
  mount = "/var/lib/postgresql/data"
  [[mounts]]              # a relative source is a package asset (Task 8 writes it to .railswitch/assets/postgres/…)
  source = "initdb/restore-dump.sh"
  target = "/docker-entrypoint-initdb.d/restore-dump.sh"
  options = "ro,z"
  [[mounts]]
  source = "{dumps}"
  target = "/db-dumps"
  options = "ro,z"
  [[ports]]
  name = "db"
  env = "DB_PORT"
  base = 55432
  container = 5432
  [unit]
  shm_size = "256mb"
  exec = ["postgres", "-c", "fsync=off"]   # full -c list copied from fd7336b db@.container
  ```

  Fields the other options need:
  - playwright: `firewall = "self"`, `[unit] add_capability = ["NET_ADMIN","NET_RAW"]`, `run_init = true`, `shm_size = "1g"`, `entrypoint = "entrypoints/entrypoint-playwright.sh"`, `[[mounts]] source = "init-firewall-playwright.sh"`, and a **standalone build** (D31 "playwright stays standalone"): `[image] build = "Containerfile.playwright"`, `tag = "{playwright}"`, `args = { PLAYWRIGHT_VERSION = "{playwright}" }`. A standalone build is not on the base chain: no `BASE_IMAGE`, and it is ejected like a tool (`containerfiles/Containerfile.playwright`, found by filename);
  - rustfs: `[[volumes]] seed = "rustfs_data.tar.gz"` (D18);
  - sidekiq: `slot = "jobs"`, `[image] use = "app"` (runs the app's base image), `command = "bundle exec sidekiq"`, `needs = ["cache"]`, `works_with = ["rails","hanami"]`.

  An option's `[image]` table has exactly one of `pull` (a registry ref template), `use = "app"`, or `build` (+ `tag`, `args`); any other combination is a `Load` error.

  `tool.toml` for nvim: `[image] file = "Containerfile.nvim"` (built on the base, D31), plus from `fd7336b` `nvim@.container`: port 17000 → 7777, `firewall = ["agent","deps"]`, `success_exit_status = 1`, and these mounts only: the worktree at `/app`, the base worktree's `.git`, the per-worktree home, the shared cache volumes, and `<prefix>_nvim_config`/`<prefix>_nvim_share`. Not ported (D33): `~/.ssh`, the SSH agent socket, `SSH_AUTH_SOCK`, the host gitconfig and gitignore. Also not ported: the kitty socket and `/usr/bin/kitten` (machine-local mounts, Stage 4).

- [ ] **Step 1: Write failing tests.**
  - `TestLoadBuiltins`: no error; the catalog has `rails@1`, `postgres@1`, `redis@1`, `rustfs@1`, `playwright@1`, `sidekiq@1` and tool `nvim`.
  - `TestParseRef`: `"rails@1"` parses; `"rails"`, `"rails@x"` and `"@1"` give errors.
  - `TestContentHashStable`: two `fstest.MapFS`s with the same files inserted in a different order hash equal; changing one byte changes the hash.
  - `TestUnknownFieldInPackToml`: a strict-decode error.
  - `TestPortBasesUnique`: two options declaring base 55432 make `Load` fail.
  - `TestRailsPackGeneric`:
    - the rails pack's `[env]` has exactly `RAILS_ENV` and `BINDING` (no `DEV_HOSTS`, no `DOMAIN`);
    - `defaults.browser == "none"`;
    - `app.health` is set;
    - `deps.frozen_env["BUNDLE_FROZEN"] == "true"`.
  - `TestPlaywrightOptionEnv`: the playwright option's env has no `HEADLESS_SYSTEM_TESTS`. It keeps only what it defines itself: `PLAYWRIGHT_HOST = "ws://{service}:8888/connect"` in `[gives]` and `CHROME_START_URL = "http://{worktree.host}"` in `[env]`.
  - `TestBuiltinFromQualified` (D36): every `FROM` in every built-in Containerfile, other than `${BASE_IMAGE}`, `scratch` and stage names, starts with a registry host (`docker.io/`, `ghcr.io/`, `quay.io/`, `mcr.microsoft.com/`).
  - `TestOptionImageKinds`: an option `[image]` with both `pull` and `use` is a `Load` error; the playwright option has `Image.Build == "Containerfile.playwright"`; sidekiq has `Image.Use == "app"`.
- [ ] **Step 2:** Run `go test ./internal/pack/`. Expected: FAIL.
- [ ] **Step 3: Implement the loader and hash.**
- [ ] **Step 4: Write the rails assets.** They are proved working in Task 21, not here.
  - `Containerfile.base` is the rails pack's **default** base Containerfile (D31):
    - `ARG RUBY_VERSION`, `ARG NODE_VERSION`;
    - a helper stage `FROM docker.io/library/node:${NODE_VERSION}-slim AS node`, used only to copy node in;
    - then `FROM docker.io/library/ruby:${RUBY_VERSION}-slim` with the OS packages a Rails app needs, including `curl` for the health check. Database clients come from the database option's addon;
    - `appuser` uid/gid 1000, and pre-created `/usr/local/bundle`, `/home/appuser/.npm`, `/home/appuser/.npm-global`, `/home/appuser/.cache/ms-playwright`.

    `pack.toml [image]` gets `os = "debian"`. Every `FROM` is fully qualified (D36). Port the generic parts of `fd7336b` `.container-config/Dockerfile` `base` stage; drop the Filial-specific packages and every Claude Code install.
  - `options/postgres/Containerfile.pg-client`: `ARG BASE_IMAGE`, `FROM ${BASE_IMAGE}`, `USER root`, `apt-get install -y --no-install-recommends libpq-dev postgresql-client`, `USER appuser`.
  - `tools/nvim/Containerfile.nvim`: `ARG BASE_IMAGE`, a build stage porting `fd7336b`'s `build-nvim` (Neovim from source on `docker.io/library/debian:trixie-slim`), then `FROM ${BASE_IMAGE}` with the built Neovim copied in. No agent software (D30).
  - `options/playwright/Containerfile.playwright` (standalone, D31): `ARG PLAYWRIGHT_VERSION`, `FROM mcr.microsoft.com/playwright:v${PLAYWRIGHT_VERSION}-noble` (it ships the browsers), then `USER root`, the packages `init-firewall-playwright.sh` needs (`nftables`, `iproute2`), as in `fd7336b`'s playwright image. No `COPY`/`ADD` from the build context: its entrypoint and firewall script are bind-mounted package assets (Task 8).
  - `entrypoint-deps.sh`: port `fd7336b` `entrypoint-deps.sh`, lockfile-frozen (D35):
    - `bundle check || bundle install`. With `BUNDLE_FROZEN=true` from the service env, bundler refuses to change `Gemfile.lock`;
    - keep the `node_modules/.deps-stamp` check, and install with `npm ci`. If `package-lock.json` is missing, exit 1 with `no package-lock.json: run railswitch deps -- npm install`;
    - install `foreman` only when `Procfile.dev` exists;
    - drop `retest`: a project that needs it supplies its own entrypoint (D13).
  - `entrypoint-app.sh`: port `fd7336b` (`bin/rails db:prepare`, then `exec "$@"`).
- [ ] **Step 5:** Run the tests. Expected: PASS.
- [ ] **Step 6:** `make check && git commit -am "feat(pack): pack/option format, built-ins, content hash"`.

### Task 5: Placeholder expansion

**Files:**
- Create: `internal/pack/expand.go`, `internal/pack/expand_test.go`

**Interfaces:**
- Consumes: `placeholder.Pattern` (Task 3); `Expand` finds placeholders only through it, so the grammar lives in one place.
- Produces:
  - `type Scope interface{ Lookup(name string) (string, bool) }`
  - `func Expand(s string, sc Scope) (string, error)`: placeholders are `placeholder.Pattern` matches (`\{[A-Za-z_][A-Za-z0-9_.]*\}`), and `{{` is a literal `{`. An unknown name returns `*UnknownPlaceholderError{Name}`. `ErrUnset` is returned for an `env.X` name the scope knows is unset.
  - `func Major(v string) string`: the leading digits before the first `.` or `-`.
  - `type MapScope map[string]string`

- [ ] **Step 1: Write failing tests.**
  - `{worktree.host}/admin` with `{"worktree.host":"feat.filial.localhost"}` gives `feat.filial.localhost/admin`.
  - `/data/rustfs{0..3}` is unchanged.
  - `http://$${1}/x` is unchanged.
  - `{{literal}` gives `{literal}`.
  - `{worktree.hots}` gives an `UnknownPlaceholderError` naming `worktree.hots`.
  - `Major`: `"16.4"` → `"16"`, `"1.0.0-alpha.83"` → `"1"`, `"8"` → `"8"`.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(pack): placeholder expansion"`.

### Task 6: Version chains

**Files:**
- Create: `internal/version/chain.go`, `internal/version/parsers.go`, `internal/version/chain_test.go`, `internal/version/testdata/`

**Interfaces:**
- Consumes: `safefile.Read`
- Produces:
  - `type Resolved struct{ Value, Source string; Warnings []string }`. `Source` is `"<file>"`, `"<file>:<key>"` or `"fallback"`.
  - `func Resolve(dir string, from []string, fallback string, valid *regexp.Regexp) Resolved`: a source value that doesn't match `valid` is skipped with a warning containing `invalid`.
  - `var ValidVersion = regexp.MustCompile(`^[0-9][0-9A-Za-z._-]{0,39}$`)`: runtimes and pins.
  - `var ValidDBName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)`: `[db_name]` (D16).

- [ ] **Step 1: Write failing tests**, one per parser, with fixtures in `testdata/`:

  | Source | File contents | Result |
  |---|---|---|
  | `.ruby-version` | `ruby-3.3.6\n` | `3.3.6` |
  | `.nvmrc` | `v22.11.0` | `22.11.0` |
  | `.tool-versions:nodejs` | `ruby 3.3.6\nnodejs 22.11.0` | `22.11.0` |
  | `Gemfile` | `ruby "3.3.6"` | `3.3.6` |
  | `Gemfile` | `ruby file: ".ruby-version"` | skipped |
  | `Gemfile.lock` | `RUBY VERSION\n   ruby 3.3.6p108` | `3.3.6` |
  | `Gemfile.lock:playwright-ruby-client` | `    playwright-ruby-client (1.54.0)` | `1.54.0` |
  | `package.json` | `engines.node` `"22.11.0"` | `22.11.0` |
  | `package.json` | `">=20"` | skipped |
  | `config/database.yml:development.database` | | port of `dev-db-name.sh`: values with `<%` or `ENV` are skipped |

  Hostile cases (Review Focus 1):
  - a symlinked `.ruby-version` is skipped with a warning containing `refused`;
  - `.ruby-version` = `3.3.6; rm -rf ~` is skipped with a warning containing `invalid`;
  - a 2 MiB `.nvmrc` is skipped;
  - when every source is skipped, the result is `Value == fallback` and `Source == "fallback"`.
  - `TestDBNameValidator`: `config/database.yml` with `development: database: filial_dev` and `ValidDBName` gives `filial_dev`; with `ValidVersion` it is skipped as `invalid`; `database: "x; drop"` with `ValidDBName` is skipped as `invalid`.
- [ ] **Step 2:** Run `go test ./internal/version/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
  - Choose the parser by base filename; the `:key` suffix is optional.
  - JSON is parsed with `encoding/json`.
  - `uv.lock:<pkg>` (TOML `[[package]] name/version`) and `package-lock.json:<pkg>` (`packages["node_modules/<pkg>"].version`) are needed by Stage 5 packs; include them now, with one test each.
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(version): per-worktree version chains with hostile-file handling"`.

### Task 7: Machine registry and `stack.lock`

**Files:**
- Create:
  - `internal/machine/dirs.go`, `registry.go`, `registry_test.go`
  - `internal/config/lock.go`, `lock_test.go`
  - `internal/config/generated.go`, `generated_test.go` (the D17 generated values; not called secrets)

**Interfaces:**
- Produces (machine):

  ```go
  type Dirs struct{ Config, Data, Quadlet string }   // from XDG env, then $HOME
  func DefaultDirs() (Dirs, error)
  type ProjectEntry struct{ Prefix, Workspace string; Subnet int }    // n in 10.214.n.0/24
  type WorktreeEntry struct{ Prefix, Slug string; ID int }
  type Registry struct{ Schema int; Projects []ProjectEntry; Worktrees []WorktreeEntry } // Open writes schema 1 into a new file and refuses a higher one (D39)
  type Locked struct{ *Registry /* unexported path + lock fd */ }
  func Open(dataDir string) (*Locked, error)      // flock LOCK_EX on registry.lock; creates registry.toml if missing
  func (l *Locked) Save() error                   // write temp + rename
  func (l *Locked) Close() error
  func (r *Registry) AddProject(prefix, workspace string) (ProjectEntry, error) // ErrPrefixTaken; lowest free n in 0..255
  func (r *Registry) AddWorktree(prefix, slug string) (WorktreeEntry, error)    // ID = max(all IDs)+1, first ID 1; ErrSlugTaken
  func (r *Registry) RemoveWorktree(prefix, slug string) error
  func (r *Registry) Project(prefix string) (ProjectEntry, bool)
  func (r *Registry) WorktreesOf(prefix string) []WorktreeEntry               // sorted by ID
  func Subnet(n int) string; func Gateway(n int) string; func TraefikIP(n int) string; func IPRange(n int) string
  ```

- Produces (config):

  ```go
  type Lock struct {
      Schema  int                  `toml:"schema"`   // 1 (D39)
      Packs   map[string]LockEntry `toml:"pack"`     // key = name
      Options map[string]LockEntry `toml:"option"`
      Images     []LockImage       `toml:"image"`      // pulled service images (D15)
      BaseImages []LockImage       `toml:"base_image"` // FROM images of built steps, by first pull (D36)
  }
  type LockEntry struct{ Version, Source, Commit, SHA256 string }
  type LockImage struct{ Ref, Digest string }
  func ReadLock(path string) (*Lock, error)       // a missing file is ErrNoLock
  func (l *Lock) Write(path string) error         // header "# Written by railswitch. Do not edit; review updates in the dashboard."
  func (l *Lock) Digest(ref string) string
  func (l *Lock) SetDigest(ref, digest string)     // adds or replaces the [[image]] entry for ref; entries kept sorted by ref
  func (l *Lock) BaseDigest(ref string) string
  func (l *Lock) SetBaseDigest(ref, digest string)
  func ReadGenerated(path string) (map[string]string, error) // [env] string values of the workspace-root mise.local.toml (D17)
  ```

- [ ] **Step 1: Write failing tests.**
  - Registry:
    - IDs are machine-wide: projects `a` and `b` alternating get 1, 2, 3;
    - after removing ID 2, the next ID is 4 (max+1, never reused while higher IDs exist);
    - a second `AddProject("a")` gives `ErrPrefixTaken`;
    - three projects get subnets 0, 1, 2;
    - `TestOpenBlocks`: a second `Open` from a goroutine blocks until the first `Close` (assert ordering with channels);
    - `Subnet(3) == "10.214.3.0/24"`, `TraefikIP(3) == "10.214.3.2"`, `IPRange(3) == "10.214.3.128/25"`.
  - Lock:
    - write/read round trip;
    - two `Write`s of the same lock are byte-identical;
    - `Digest` on an unknown ref returns `""`;
    - `SetDigest("docker.io/library/postgres:16", "sha256:aa")` then `Write`/`ReadLock` gives `Digest(…) == "sha256:aa"`; a second `SetDigest` with `"sha256:bb"` replaces it (one entry).
  - Generated:
    - reads `[env] DB_PASSWORD = "x"`;
    - ignores non-string values;
    - a missing file gives an empty map and no error (render reports missing names).
  - Schemas (D39):
    - a new `registry.toml` has `schema = 1`;
    - `registry.toml` or `stack.lock` with `schema = 2` gives an error containing `newer railswitch`.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat: machine registry, stack.lock, generated values"`.

### Task 8: Resolve the stack and build the render model with env layers

**Files:**
- Create:
  - `internal/pack/resolve.go`, `resolve_test.go`
  - `internal/render/model.go`, `env.go`, `images.go`, `model_test.go`, `images_test.go`
  - `testdata/fixtures/filial/mise.local.toml`: `[env]` then `DB_PASSWORD = "s3cret"` and `S3_SECRET_KEY = "s3cret-s3"`;
  - `testdata/fixtures/filial/db-dumps/rustfs_data.tar.gz` (a valid empty tar.gz; Task 18's `volume import` reads it);
  - `testdata/fixtures/filial/master/` and `testdata/fixtures/filial/feat/` (D22 layout: `<workspace>/<slug>/`), each with `.ruby-version` (`3.3.6`), `.nvmrc` (`22.11.0`), `Gemfile.lock` (`playwright-ruby-client (1.54.0)`), and `config/database.yml` (`development: database: filial_dev`).
- Modify:
  - `testdata/fixtures/filial/project.toml` (from Task 3): add `[tools] nvim = true`, `[worktrees.feat.versions] postgres = "17"`, `[firewall] allow.deps = ["github.com"]`, and `addons = ["containerfiles/Containerfile.pg-client"]` under `[image]`. (The §3.2 example already has `browser = "playwright@1"` and `[env] TZ = "Asia/Bangkok"`.)

    The fixture has no `containerfiles/`. The test helper that copies the fixture to a temp workspace writes the built-in defaults' bytes there (`Containerfile.base`, `.pg-client`, `.nvim`, `.playwright`), so the fixture can't drift from the built-ins. The copy includes `db-dumps/` (mode 0700), as `new`/`init` create it (Task 20).

**Interfaces:**
- Consumes: `config.Project`, `pack.Catalog`, `pack.Expand`, `version.Resolve` with `version.ValidVersion` (runtimes, pins) and `version.ValidDBName` (`[db_name]`), `config.Lock`, `machine.ProjectEntry`/`WorktreeEntry`
- Produces:

  ```go
  // pack
  type Stack struct {
      Pack    *Pack
      Options map[string]*Chosen // slot → chosen; "none" slots absent
      Tools   []*Tool
  }
  type Chosen struct{ Option *Option; Service string }
  func (c *Catalog) Resolve(p *config.Project) (*Stack, error)
  // Errors: unknown ref (lists available refs for the slot); works_with excludes the pack;
  // needs unmet ("sidekiq needs cache"); a duplicate service id.

  // render
  type Layer int // LayerPack=1, LayerOption, LayerProject, LayerService, LayerWorktree, LayerSecret
  type EnvVar struct{ Name, Value string; Layer Layer; Source string }
  type Worktree struct{ Slug string; ID int; Dir, Branch string }
  type Host struct{ CPUs int; Home, NetworkDir, GitName, GitEmail string } // GitName/GitEmail: host `git config --global user.name/user.email`, read by workspace.Render
  type Inputs struct {
      Workspace string
      Project   *config.Project
      Stack     *pack.Stack
      Lock      *config.Lock
      Machine   machine.ProjectEntry
      Worktrees []Worktree
      Generated map[string]string // D17 values from mise.local.toml
      Host      Host
  }
  type Model struct {
      Prefix   string
      Services []Service // ordered: app, deps, worker, data (slot order), tools
      Inputs   Inputs
      Warnings []string  // non-fatal findings, e.g. a suggested option addon missing from [image] addons
      Assets   []Asset   // every package asset a service mounts; Task 11's Plan writes them
  }
  type Asset struct {
      Rel  string      // workspace-relative: ".railswitch/assets/<package>/<path>", e.g. ".railswitch/assets/postgres/initdb/restore-dump.sh"
      Data []byte      // the embedded package file's bytes
      Mode fs.FileMode // 0755 for scripts (*.sh), else 0644
  }
  type Service struct {
      ID, Role   string   // app|deps|worker|data|tool
      Image      ImageSpec // one of: Pull{Ref template}; Base (the worktree's base image); Build{File, Args} on the base (D31); Standalone{File, Tag, Args} (an option's own build, not on the chain: playwright)
      Exec       []string
      Entrypoint string
      Command    string
      Networks   []string // "dev", "proxy"
      Publish    []Port
      Volumes    []Volume
      Mounts     []Mount
      Routes     []Route
      Health     *Health
      Requires   []string // service ids
      Wants      []string
      Firewall   []string // profiles; ["self"] = no hook
      Oneshot    bool
      Unit       UnitExtras
      EnvSpec    []EnvVar // unexpanded templates with layer provenance
  }
  func Build(in Inputs) (*Model, error)
  type Instance struct {
      Worktree Worktree
      Values   map[string]string   // every placeholder name → its concrete value for this worktree
      Env      map[string][]EnvVar // svc → effective process env, highest layer per name, provenance kept
      Builds   []BuildStep         // in build order: the base chain, then tool images, then standalone option images
  }
  type BuildStep struct {
      Name    string            // "base", "addon pg-client", "nvim", "playwright"
      File    string            // absolute path, always under <ws>/containerfiles/
      Context string            // always <ws>/containerfiles
      Args    map[string]string // includes BASE_IMAGE for every chain/tool step after the first; never for a standalone step
      Tag     string            // D9
      From    []string          // FROM refs of this file (ARGs expanded), excluding ${BASE_IMAGE}, scratch and stage names (D36)
  }
  func (m *Model) Instance(wt Worktree) (*Instance, error)
  type Names struct{ Units, Containers, Volumes, Networks []string }
  func (m *Model) NamesFor(slug string) (Names, error) // exact names of one registered slug's objects; an unregistered slug is an error
  // Units: every "<prefix>-<slug>-<svc>.service" and "<prefix>-<slug>-dev-network.service";
  // Containers: "<prefix>-<slug>-<svc>"; Volumes: per-worktree volumes only ("<prefix>-<slug>-<name>", never "<prefix>_…");
  // Networks: "<prefix>-<slug>-dev". Used by wt rm (Task 17), stop/down (Task 18) and secret set (Task 16A).
  ```

- Rules:
  - **Services:** the app service (`[stack] app`, default the pack's `app.service`, role app, command `app.server`, the base image); `deps` (role deps, oneshot, the base image, `deps_entrypoint`, plus the pack's `[deps] frozen_env` at the pack layer); one service per chosen option (role `data` for database/cache/storage, `worker` for jobs unless the image is in-process, `tool` for mail/browser; an option whose `Image.Use == "app"` (`[image] use = "app"`) runs the base image); tool `nvim` only when `[tools] nvim = true`.
  - **Dependencies:** the app Requires deps and every data service, and Wants every tool (D23).
  - **Networks:** app and routed services join `dev` and `proxy`; the rest join `dev` only.
  - **Env layers** apply per HANDOFF §3.7, with D10/D11 deciding which services get which layers:
    - pack env goes to app, worker, deps and tools;
    - option `gives` goes to app, worker and tools;
    - option `env` goes to its own service;
    - `[env]` goes to every service except deps;
    - `[services.<id>.env]` goes to that service;
    - the worktree layer (`WORKTREE_ID`, `WORKTREE_SLUG`, `WORKTREE_HOST`, the port env of ports the service publishes) goes to app, worker and tools, plus each service's own ports;
    - generated values are referenced only through `{generated.X}` inside values.
  - **Instance values:** `Values` holds every name a unit field can use:
    - `worktree.slug`, `worktree.host`, `traefik.ip`, `branch`;
    - `project.prefix`;
    - `image.<svc>`;
    - each pin or runtime and its `.major`;
    - each port env name;
    - `dumps`: `<ws>/db-dumps`;
    - `db`: the `[db_name]` chain result (`version.Resolve` with `ValidDBName`, fallback expanded with `project.prefix`);
    - `env.<NAME>`: the value of `NAME` from env layers 1–3 (pack `[env]`, option, project `[env]`); a name absent there is unset (`ErrUnset`);
    - `secret.<name>`: the podman secret name (`<prefix>_<name>` or `railswitch_<name>`, D33).

    `generated.*` is never in `Values`; it is resolved only into `Env`.
  - **Per-service scope:** expanding a service's fields and env adds, on top of `Values`: `service` (that option's service id; for `[gives]`, the giving option's service id) and `scheme` (the pack's `db_scheme[<option name>]`, e.g. `postgres://`). An env var whose value expands to `ErrUnset` is omitted from that service's env, with no error (e.g. `PGTZ = "{env.TZ}"` with no `TZ` set). Any other unknown name is an `UnknownPlaceholderError`.
  - **Package assets (D12/D13):** a relative `[[mounts]] source`, an `entrypoint` or a `deps_entrypoint` names a file in its package (`rails`, `postgres`, `playwright`, `nvim`, …). Each used asset becomes one `Model.Assets` entry at `.railswitch/assets/<package>/<path>`. The service mounts it read-only: a `[[mounts]]` asset at its `target`; an entrypoint at `/usr/local/bin/<basename>`, with `Entrypoint=/usr/local/bin/<basename>`. When `<ws>/entrypoints/<basename>` exists, the mount source is that file instead (D13: the project's own entrypoint wins), and no asset is written for it.
  - **Secret env collisions:** a secret delivered as env (`env = "X"`) to a service that also gets `X` from layers 1–5 is an error naming the secret, the service and `X`. The precedence between podman's `--env-file` and a `type=env` secret is not something to rely on.
  - **Secrets with `git_host` (D33):** the services named in that secret's `services` get git wiring through `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_n`/`GIT_CONFIG_VALUE_n` env, so nothing is written into the repo's `.git/config`, which the host shares:
    - `credential.https://<git_host>.helper` = `!f() { test "$1" = get && printf 'username=<git_user>\npassword=%s\n' "$(cat /run/secrets/<name>)"; }; f`. With `env` delivery, it prints `"$<ENV>"` instead of reading the file;
    - `url.https://<git_host>/.insteadOf` = `git@<git_host>:`, so ssh remotes go over HTTPS inside the container.

    Those services also get `GIT_AUTHOR_NAME`/`EMAIL` and `GIT_COMMITTER_NAME`/`EMAIL` from `Host`. railswitch handles only the podman secret's name, never its value.
  - **Image:**
    - a pulled image is `ref`, or `ref@digest` when `Lock.Digest(ref) != ""`;
    - **the base chain (D31):** step 1 is `[image] base`; then each `[image] addons` entry, in listed order. Only workspace files are ever built; railswitch never builds a built-in Containerfile directly;
    - each step after the first gets `BASE_IMAGE=<previous step's tag>`;
    - `image.base` is the last step's tag; app, deps and worker run it;
    - each enabled tool is a further step on `image.base`, built from `containerfiles/Containerfile.<tool>` (`Containerfile.nvim`);
    - **standalone builds (D31: playwright stays standalone):** an option with `[image] build = "Containerfile.<name>"` is built from `containerfiles/Containerfile.<name>` (`Containerfile.playwright`), after the chain and tools, with its own `[image] args` (`PLAYWRIGHT_VERSION = "{playwright}"`) and no `BASE_IMAGE`. Its tag is `localhost/<prefix>/<name>:<expanded [image] tag>-<hash8>` (`localhost/filial/playwright:1.54.0-<hash8>`; hash8 = first 8 hex of the sha256 of the Containerfile and its sorted args, D9); `image.<svc>` is that tag. Its `From` refs are pinned like every other step's (D36);
    - a missing file in the chain, for an enabled tool or for a chosen standalone option is an error naming it and `railswitch containerfile eject <name>`;
    - a chosen option whose `[app_addon]` file isn't in `[image] addons` is a warning, not an error (`Model.Warnings []string`): `postgres@1 suggests addon pg-client, which is not in [image] addons (railswitch containerfile eject pg-client), ignore this if your base already covers it`.

    Per-worktree `[worktrees.<slug>.versions]` overrides `[versions]`, which overrides the option's pin default.
  - **Containerfile contract**, checked on every step after the first and on every addon and tool file (in `internal/render/images.go`). A standalone step gets only the `USER` and fully-qualified-`FROM` checks. Each failure is an error naming the file:
    - `ARG BASE_IMAGE` comes before the first `FROM`, and the last `FROM` is exactly `FROM ${BASE_IMAGE}`. Helper stages before it are allowed;
    - if the file has a `USER` line, the last one is `USER appuser`, `USER 1000` or `USER 1000:1000` (keep-id maps the host user to 1000);
    - for every step, the first included: every other `FROM` is fully qualified (`<registry>/<repo>:<tag>` or `…@sha256:…`), after expanding `${ARG}`s from the step's args and `ARG` defaults. `scratch` and stage names are allowed. Short names resolve differently per host's `registries.conf` and can't be pinned (D36);
    - (the `os` match between an option addon and the pack is checked when the addon is ejected, Task 12A).

- [ ] **Step 1: Write failing tests.**
  - `TestResolveErrors`: `browser = "nope@1"` gives an error listing `playwright@1`; `jobs = "sidekiq@1"` with `cache = "none"` gives `sidekiq needs cache`.
  - `TestFixedServiceIDs`: the Filial fixture yields services `rails`, `deps`, `db`, `cache`, `storage`, `browser`, `nvim`, and app env `REDIS_URL == "redis://cache:6379/1"`.
  - `TestLayerPrecedence`: `[env] TZ = "Asia/Bangkok"` plus `[services.rails.env] TZ = "UTC"` gives an effective rails TZ of `UTC` with `Layer == LayerService`; the db service TZ is `Asia/Bangkok`.
  - `TestDatabaseURL`: the effective app `DATABASE_URL` is `postgres://postgres:s3cret@db/filial_dev` (fixture database.yml has `filial_dev`, not the `filial_development` fallback, so the chain is proven live).
  - `TestEnvScope` (placeholder scope): the db service has `PGTZ=Asia/Bangkok` from `{env.TZ}`; with `[env] TZ` removed, `Build`/`Instance` give no error and db has no `PGTZ`; the playwright `[gives]` `PLAYWRIGHT_HOST` is `ws://browser:8888/connect`; postgres' `/db-dumps` mount source is `<ws>/db-dumps`.
  - `TestDepsGetsNoSecrets`: no deps EnvVar value contains the secret value, and no deps EnvVar is named `DATABASE_URL`.
  - `TestMissingSecret` (Review Focus 3): with `DB_PASSWORD` absent from `mise.local.toml`, `Build` or `Instance` errors with a message containing `DB_PASSWORD` and `mise.local.toml`.
  - `TestValuesHaveNoSecrets`: no `Instance.Values` value contains the secret.
  - `TestWorktreeVersionOverride`: for `feat`, `Values["image.db"] == "docker.io/library/postgres:17"` and `Values["postgres.major"] == "17"`; master stays 16.
  - `TestDigestPin`: a lock digest for `docker.io/library/postgres:16` gives a master `Values["image.db"]` ending in `@sha256:…`.
  - `TestSecretGitWiring` (D33): `[secrets.github_token] services = ["nvim"]`, `git_host = "github.com"`:
    - nvim's env has `GIT_CONFIG_COUNT=2` with a `credential.https://github.com.helper` that reads `/run/secrets/github_token`, and `url.https://github.com/.insteadOf=git@github.com:`;
    - nvim's env has `GIT_AUTHOR_NAME`, `GIT_AUTHOR_EMAIL`, `GIT_COMMITTER_NAME`, `GIT_COMMITTER_EMAIL` from `Host`;
    - `rails`, `deps` and `db` get none of these.
  - `TestNoGitTokenEnv`: with `[secrets.github_token] services = ["nvim"]`, nvim's `Instance.Env` has no `GH_TOKEN` or `GITHUB_TOKEN`, and its git credential helper reads `/run/secrets/github_token`.
  - `TestSecretEnvCollision`: `[secrets.tz] services = ["rails"]`, `env = "TZ"` with `[env] TZ = "Asia/Bangkok"` gives an error naming `tz`, `rails` and `TZ`.
  - `TestPortsFromID`: ID 7 gives `DB_PORT=55439`, `RUBY_DEBUG_PORT=33007`, `NVIM_PORT=17007`, `PLAYWRIGHT_HOST_PORT=18895`, `APP_PORT=13007`, `S3_PORT=19007`.
  - `TestImageChain` (D31):
    - with `[image] addons = ["containerfiles/Containerfile.pg-client", "containerfiles/Containerfile.vips"]`, master's `Builds` names are `base`, `addon pg-client`, `addon vips`, `nvim`, `playwright`, in that order, and every `File` is under the workspace's `containerfiles/`;
    - each chain/tool step's `Args["BASE_IMAGE"]` equals the previous chain step's `Tag` (nvim's equals `image.base`); the `playwright` step has no `BASE_IMAGE`, `Args["PLAYWRIGHT_VERSION"] == "1.54.0"`, `Tag` starting with `localhost/filial/playwright:1.54.0-`, `From == ["mcr.microsoft.com/playwright:v1.54.0-noble"]`, and `Values["image.browser"]` equals that tag;
    - `rails` and `deps` use `Values["image.base"]`, which equals the `addon vips` tag and starts with `localhost/filial/base:ruby3.3.6-node22.11.0-`;
    - `nvim`'s tag starts with `localhost/filial/nvim:ruby3.3.6-node22.11.0-`;
    - changing one byte of `containerfiles/Containerfile.vips` changes the `addon vips` and `nvim` tags only;
    - changing one byte of `containerfiles/Containerfile.base` changes every chain and tool tag, and not the `playwright` tag.
  - `TestImageChainMissingFile`: deleting `containerfiles/Containerfile.nvim` gives an error containing `railswitch containerfile eject nvim`; with `[tools] nvim = false` there is no error. Deleting `containerfiles/Containerfile.playwright` gives an error containing `railswitch containerfile eject playwright`; with `browser = "none"` there is no error.
  - `TestAssets` (D12/D13): `Model.Assets` has `.railswitch/assets/rails/entrypoints/entrypoint-app.sh` (0755, the embedded bytes) and `.railswitch/assets/postgres/initdb/restore-dump.sh`; with `<ws>/entrypoints/entrypoint-app.sh` present, the rails entrypoint mount source is that file and no `rails/entrypoints/entrypoint-app.sh` asset is listed.
  - `TestNamesFor`: for `feat`, `Units` contains `filial-feat-rails.service` and `filial-feat-dev-network.service`; `Containers` contains `filial-feat-db`; `Volumes` is exactly the feat per-worktree volumes (`filial-feat-db-data-pg17`, `filial-feat-node-modules`, …) and none starts with `filial_`; `Networks == ["filial-feat-dev"]`. `NamesFor("gone")` is an error.
  - `TestOptionAddonWarning`: postgres chosen with `addons = []` gives no error, and one warning containing `pg-client` and `eject`.
  - `TestFromQualified`:
    - a base file with `FROM ruby:3.3-slim` gives an error naming the file and `fully qualified`;
    - for the default base, master's base step has `From == ["docker.io/library/node:22.11.0-slim", "docker.io/library/ruby:3.3.6-slim"]`.
  - `TestAddonContract`: an addon whose last `FROM` is `debian:trixie` gives an error naming the file and `FROM ${BASE_IMAGE}`; an addon ending `USER root` gives an error naming `USER`.
- [ ] **Step 2:** Run `go test ./internal/pack/ ./internal/render/`. Expected: FAIL.
- [ ] **Step 3: Implement.** `Model.Services` keeps placeholders unexpanded. `Instance` builds the per-worktree scope (a `pack.MapScope` of `Values`, plus `generated.*` for env only) and expands each service's `EnvSpec` with it. Task 9's writer expands unit fields with the same `Values`.
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(render): stack resolution and layered env model"`.

### Task 9: Quadlet writer with golden tests

**Files:**
- Create:
  - `internal/render/quadlet.go`, `internal/render/quadlet_test.go`, `internal/render/testdata/golden/filial/*`, `internal/render/testdata/golden/minimal/*`
  - `testdata/fixtures/minimal/`: `project.toml` (`schema = 1`, `[project] name = "minimal"`, `prefix = "mini"`, `[stack] pack = "rails@1"`, `[options] database = "none"`, `cache = "none"`, `browser = "none"`, no `[tools]`, `[image] base = "containerfiles/Containerfile.base"`) and `master/` with `.ruby-version` (`3.3.6`) and `.nvmrc` (`22.11.0`). The Task 8 fixture helper copies it the same way (writing `containerfiles/Containerfile.base` from the built-in).

**Interfaces:**
- Consumes: `Model`
- Produces:
  - `func Quadlet(m *Model) (map[string][]byte, error)`: keyed by filename, for every worktree in `m.Inputs.Worktrees`. It returns `<prefix>-proxy.network`, plus for each worktree `<prefix>-<slug>-dev.network` and one `<prefix>-<slug>-<svc>.container` per service.
  - `const Marker = "# railswitch: rendered from "`. The first line of every file is `Marker + <workspace>/project.toml (prefix <prefix>, worktree <slug>). Edit project.toml, not this file.` `<prefix>-proxy.network` omits `, worktree <slug>`. The second line of every `.container` is `# role: <role>` (`app`, `deps`, `worker`, `data` or `tool`); Task 10's `deps-remain` lint reads it.

- Emission rules (each is a HANDOFF §7 gotcha, and Task 10 lints every one):
  - **Network dependencies (D27):** `Network=` always names the `.network` unit (`<prefix>-<slug>-dev.network`, `<prefix>-proxy.network`), so Quadlet adds the `Requires=`/`After=` itself.
  - **Aliases:** scoped on the dev network only: `Network=<prefix>-<slug>-dev.network:alias=<svc>`.
  - **Proxy network:** `NetworkName=<prefix>_proxy`, with `Subnet`, `Gateway` and `IPRange` from `machine`. Dev network: `NetworkName=<prefix>-<slug>-dev`, `Options=mtu=1400`.
  - **Env files:** every container has `EnvironmentFile=<ws>/.railswitch/env/<slug>/<svc>.env` in `[Container]`. There is nothing under `[Service]` for systemd to expand (D8).
  - **Images:** `Image=` is `Values["image.<svc>"]`: a pulled ref (with `@digest` when locked), the base chain's tag, a tool's tag, or a standalone build's tag (the browser: `Image=localhost/filial/playwright:1.54.0-<hash8>`).
  - **Package assets (Task 8 `Model.Assets`):** each asset mount is `Volume=<ws>/.railswitch/assets/<package>/<path>:<target>:ro,z` (or `<ws>/entrypoints/<basename>` when the project supplies it, D13); an entrypoint asset adds `Entrypoint=/usr/local/bin/<basename>`.
  - **Data services:** health keys from the option, plus `Notify=healthy`.
  - **App:** `Requires=`/`After=` on the deps and data units; `Wants=` on the tool units and `railswitch-traefik.service`.
  - **deps:** `Type=oneshot`, `TimeoutStartSec=900`, `LogDriver=passthrough`, never `RemainAfterExit`.
  - **Other containers:** `LogDriver=k8s-file`, `LogOpt=max-size=20m`, `Restart=on-failure`, `RestartSec=5`.
  - **Publishing:** `PublishPort=127.0.0.1:<host port>:<container port>`, e.g. `127.0.0.1:55434:5432` for ID 2.
  - **Routes:**
    - `Label=traefik.enable=true`;
    - `Label=traefik.docker.network=<prefix>_proxy`;
    - router/service names `<prefix>-<slug>-<route>`;
    - the rule `Host(\`<host>\`)`;
    - `AddHost=<host>:<traefik ip>` for every routed host, on every service on the proxy network.
  - **Firewall:** unless the service's firewall is `self`:
    - `GlobalArgs=--hooks-dir=<ws>/.railswitch/firewall/hooks.d`;
    - `Annotation=<prefix>.firewall=<profiles>`;
    - `Annotation=<prefix>.firewall.networks=<comma list of the actual network names>`;
    - `Annotation=<prefix>.firewall.mode=<mode>`.
  - **CPU:** `PodmanArgs=--cpus=<n>`. App, worker and deps get `max(1, CPUs*3/4)`; tools get `max(1, CPUs/4)`.
  - **keep-id:** services that mount the worktree get `UserNS=keep-id:uid=1000,gid=1000` and `User=1000:1000`.
  - **Hardening (D34):**
    - every container gets `NoNewPrivileges=true`;
    - app, deps, worker and tool containers also get `DropCapability=all`;
    - data containers keep podman's default capabilities, because their entrypoints switch users;
    - the browser keeps its `AddCapability=NET_ADMIN`/`NET_RAW` and gets no `DropCapability`.
  - **Limits (D34):** every container gets `Memory=` and `PidsLimit=`, from the first of:
    - `[services.<id>]` `memory`/`pids_limit`;
    - the pack/option `[unit] memory`/`pids_limit`;
    - the role default: app 4g/4096, worker 2g/4096, deps 4g/4096, data 2g/1024, tool 2g/4096.
  - **App health (D34):** when the pack declares `[app] health` and `[services.<app>] health` isn't `"none"`:
    - `HealthCmd=<cmd>` (`[services.<app>] health` replaces the pack's command), `HealthInterval=10s`, `HealthRetries=3`, `HealthStartPeriod=<health_start_period>`, `Notify=healthy`;
    - `TimeoutStartSec=max(180, start period + 60s)`. A start period longer than `TimeoutStartSec` breaks `Notify=healthy` ([podman #27290](https://github.com/podman-container-tools/podman/issues/27290));
    - `systemctl start` of the app then returns only once the app answers.
  - **Escaping:** user-supplied strings (commands, exec args, link text) get `%` → `%%` and `$` → `$$`. Values with spaces are double-quoted.

- [ ] **Step 1: Write failing tests.**
  - `TestGolden`: for each fixture dir (`testdata/fixtures/filial`, `testdata/fixtures/minimal`), compare every file with `testdata/golden/<fixture>/<file>`. `go test ./internal/render -update` rewrites the goldens. Review the first goldens by hand against `fd7336b` units before committing them.
  - `TestRoleLine`: line 2 of `filial-master-deps.container` is `# role: deps`, of `filial-master-rails.container` `# role: app`, of `filial-master-db.container` `# role: data`.
  - `TestAssetMounts`: `filial-master-db.container` has `Volume=<ws>/.railswitch/assets/postgres/initdb/restore-dump.sh:/docker-entrypoint-initdb.d/restore-dump.sh:ro,z`; `filial-master-rails.container` has `Entrypoint=/usr/local/bin/entrypoint-app.sh` and `Volume=<ws>/.railswitch/assets/rails/entrypoints/entrypoint-app.sh:/usr/local/bin/entrypoint-app.sh:ro,z`; `filial-master-browser.container` has `Image=localhost/filial/playwright:1.54.0-` followed by 8 hex digits.
  - `TestDepsNotRemainAfterExit`.
  - `TestNoNonLoopbackPublish`.
  - `TestPercentEscaped` (Review Focus 5): a test catalog (`fstest.MapFS`) whose pack server is `echo 100%` gives `Exec=… 100%%`.
  - `TestPlaywrightNoHook`: the browser unit has no `GlobalArgs=--hooks-dir`.
  - `TestHardening`:
    - `rails`, `deps` and `nvim` units have `DropCapability=all` and `NoNewPrivileges=true`;
    - `db` has `NoNewPrivileges=true` and no `DropCapability`;
    - `browser` has `AddCapability=NET_ADMIN` and no `DropCapability`.
  - `TestLimits`: `rails` has `Memory=4g` and `PidsLimit=4096`; `db` has `Memory=2g` and `PidsLimit=1024`. `[services.rails] memory = "6g"` gives `Memory=6g`.
  - `TestAppHealth`:
    - `rails` has `HealthCmd=curl -fsS http://localhost:3000/up`, `HealthStartPeriod=120s`, `Notify=healthy`, `TimeoutStartSec=180`;
    - `[services.rails] health = "none"` removes every health key and `Notify=healthy` from `rails`.
  - `TestWorktreeVersionOverride`: `filial-feat-db.container` has `Image=docker.io/library/postgres:17` and `Volume=filial-feat-db-data-pg17:/var/lib/postgresql/data`; `filial-master-db.container` has `:16` and `-pg16`.
  - `TestUnitsHaveNoSecrets`: no rendered unit contains the fixture's secret values.
  - `TestNoUnitVars`: no rendered unit contains `${` except inside `$${`.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement** with `text/template` over a small per-section key/value builder, so that ordering and quoting live in one place.
- [ ] **Step 4:** Run `go test ./internal/render -run TestGolden -update`. Inspect the goldens. Then run without `-update`. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(render): Quadlet unit writer with goldens"`.

### Task 10: Render-time lints

**Files:**
- Create: `internal/render/lint.go`, `internal/render/lint_test.go`, `internal/render/testdata/lint/*.container` (one bad unit per rule, plus `machine-ok.container`)
- Modify: `internal/render/quadlet.go` (`Quadlet` runs `Lint` on its own output)

**Interfaces:**
- Produces:
  - `type Finding struct{ File string; Line int; Rule, Msg string }`
  - `func Lint(files map[string][]byte) []Finding`

  `Quadlet` must return an error wrapping all findings if `Lint` of its own output is non-empty.

- Rules, by `Rule` id (each test feeds the matching bad file and asserts exactly that `Rule`):

  | Rule | Fails when |
  |---|---|
  | `single-dollar` | a `$` not written `$$` anywhere in a unit (systemd would expand it; covers `${VAR}` and a Traefik `${1}` backreference) |
  | `unquoted-space` | a `Label=`/`Environment=` value containing a space that is not wrapped in `"` |
  | `network-dependency` | `Network=X` where X is not a `.network` unit and no `Requires=` and `After=` name X's network service |
  | `loopback-publish` | `PublishPort=` not starting with `127.0.0.1:` |
  | `firewall-networks` | the `<prefix>.firewall.networks` annotation set ≠ the set of `Network=` names (after `.network` → `NetworkName` mapping); also fails when a `GlobalArgs=--hooks-dir` exists without the profile annotation, or vice versa |
  | `shared-underscore` | a `Volume=` source that is none of: `<prefix>-<slug>-…` (prefix and slug from the marker), `<prefix>_…`, an absolute path, or a path starting with a systemd specifier (`%t/`, `%h/`). A unit whose marker says `(machine)` has no prefix/slug, so only the absolute/specifier forms (and `railswitch_…`) are accepted |
  | `deps-remain` | `RemainAfterExit=` in a unit whose second line is `# role: deps` (Task 9 writes that line in every `.container`) |

- [ ] **Step 1: Write the seven failing tests**, plus `TestLintMachineMarker` (`machine-ok.container`: marker `(machine)`, `Volume=%t/podman/podman.sock:/var/run/docker.sock:ro`, gives no finding; the same unit with `Volume=foo:/x` gives `shared-underscore`) and `TestGoldensLintClean` (`Lint` on both golden sets returns nothing).
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement** a line-oriented INI scanner that understands `\` continuations.
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(render): lints for every render-time gotcha"`.

### Task 11: Apply (write, orphan removal, check mode)

**Files:**
- Create: `internal/render/apply.go`, `internal/render/diff.go`, `internal/render/apply_test.go`, `internal/render/diff_test.go`

**Interfaces:**
- Produces:

  ```go
  type Plan struct {
      Units map[string][]byte            // → Dirs.Quadlet
      Files map[string]File              // workspace-relative path → content+mode (env files, package assets, firewall assets)
  }
  type File struct{ Data []byte; Mode fs.FileMode }
  type Diff struct{ Added, Changed, Removed []string; Text string } // Text = unified diff
  func (m *Model) Plan() (*Plan, error)   // units, every worktree's env files, and every Model.Assets entry at its Rel path with its Mode
  func Apply(p *Plan, ws, quadletDir, prefix string, check bool) (*Diff, error)
  ```

- [ ] **Step 1: Write failing tests** (temp dirs):
  - the first apply reports every file as Added;
  - a second apply reports an empty Diff and does not modify mtimes;
  - an orphan `filial-gone-rails.container` whose Marker says `(prefix filial, worktree gone)`, with `gone` not registered, is removed;
  - a marker file for prefix `billing` is untouched;
  - a hand-written `filial-x.container` without a marker is untouched;
  - env files are mode 0600 and their dirs 0700;
  - `Plan()` of the filial model has `Files[".railswitch/assets/rails/entrypoints/entrypoint-app.sh"]` with mode 0755 and the embedded bytes, and `Files[".railswitch/assets/postgres/initdb/restore-dump.sh"]`;
  - `diff_test.go`: the LCS differ gives `-a\n+b\n` for `a`→`b` and nothing for equal inputs;
  - `check=true` writes nothing and returns `Diff.Text` containing `+Image=`;
  - a write failure midway (read-only quadlet dir) leaves no `*.tmp` files and returns an error (Review Focus 4).
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.** Write each file as temp + `os.Rename`. Remove orphaned env dirs for slugs no longer registered. Produce the diff with a small LCS line differ in `internal/render/diff.go`, with no dependency.
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(render): atomic apply, orphan removal, check mode"`.

### Task 12: CLI `lock`, `render`, `env`

**Files:**
- Create:
  - `internal/workspace/workspace.go`, `workspace_test.go`: find the workspace by walking up from `-C`/cwd to the dir with `project.toml`; resolve the worktree from `-w` or cwd. `internal/cli`, `lifecycle`, `worktree` and `doctor` all use it. It never imports them, so there is no cycle.
  - `internal/cli/render.go`, `internal/cli/render_test.go`
  - `docs/json.md` (D40): the `env --json` schema; every later `--json` command adds its section

**Interfaces:**
- Consumes: Tasks 3–11
- Produces:
  - `railswitch lock`: calls `workspace.Lock(ctx, root)`. It resolves the stack and writes `stack.lock` (built-ins: `source = "built in (railswitch <Version>)"`, `sha256 = ContentHash`), keeping existing image digests.
  - `railswitch render [--check]`: `config.Load` → `Validate` → `ReadLock`. A lock entry that differs from the resolved stack exits 1 with `stack.lock is out of date for <ref>: run railswitch lock`. Then `Build` → `Plan` → `Apply`. `--check` exits 1 when the Diff is non-empty and prints `Diff.Text`.
  - `railswitch env [svc]`: prints the effective env of the current worktree, one line per variable:
    - `NAME=value  # <layer>: <source>`;
    - a value that contains a generated (D17) value prints `NAME=••••••••  # <layer>: <source>` (generated values aren't secrets, but they are never echoed);
    - a podman-secret env delivery prints `NAME  # secret: podman secret <podman name>`, with no `=` and no value (railswitch never has it).

    With `--json`: `[{"service":…,"name":…,"value":…,"layer":…,"source":…}]`; a masked value is `"••••••••"` and a secret has `"value": null`, `"layer": "secret"`, `"source": "podman secret <podman name>"`.
  - Package `workspace`, reused by every later command:

    ```go
    type Workspace struct {
        Root    string
        Project *config.Project
        Catalog *pack.Catalog
        Stack   *pack.Stack
        Lock    *config.Lock
        Dirs    machine.Dirs
        Slug    string // current worktree, "" at the root
    }
    func Load(ctx context.Context, startDir, slug string) (*Workspace, error) // needs stack.lock (ErrNoLock otherwise)
    func Lock(ctx context.Context, root string) error // Load + Validate project.toml at root, Resolve the stack, write <root>/stack.lock; needs no existing lock (used by `lock` and `new`)
    func (w *Workspace) Model(reg *machine.Registry, host render.Host) (*render.Model, error)
    func (w *Workspace) Render(ctx context.Context, r sys.Runner, check bool) (*render.Diff, error)
    // Render: opens the registry, builds the Model, merges every source into one render.Plan, and Applies it.
    // The Plan's sources are the project's Plan(), machine.ProxyUnits (Task 15) and firewall.Files (Task 16).
    // It is the only path that writes units: cli render, lifecycle Up, worktree Add/Remove and new all call it.
    ```

- [ ] **Step 1: Write failing tests**, using a temp workspace built from `testdata/fixtures/filial` (the Task 8 fixture helper) with `XDG_DATA_HOME` and `XDG_CONFIG_HOME` pointing into the temp dir. The test helper then opens the temp registry (`machine.Open`) and runs `AddProject("filial", ws)`, `AddWorktree("filial", "master")`, `AddWorktree("filial", "feat")`, `Save`, `Close`, because nothing before Tasks 17/20 registers worktrees:
  - `lock` then `render` exits 0, and the quadlet dir has `filial-master-rails.container` and `filial-feat-rails.container`;
  - `render --check` right after exits 0;
  - editing `[env]` makes `render --check` exit 1 with a diff;
  - deleting `stack.lock` makes `render` exit 1 mentioning `railswitch lock`;
  - `env rails` output has `DATABASE_URL=••••••••` (the value contains a generated password), `TZ=Asia/Bangkok  # project`, and, with the fixture's twilio secret, the line `TWILIO_AUTH_TOKEN  # secret: podman secret filial_twilio_auth_token` (no `=`);
  - `--json env rails` parses; the `TWILIO_AUTH_TOKEN` entry has `"value": null`, and the `DATABASE_URL` entry has `"value": "••••••••"`;
  - `workspace.Lock` on a temp root whose `project.toml` is valid and that has no `stack.lock` writes one.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(cli): lock, render, env"`.

### Task 12A: CLI `containerfile list|show|eject|new`

**Files:**
- Create: `internal/config/edit.go`, `internal/config/edit_test.go`, `internal/cli/containerfile.go`, `internal/cli/containerfile_test.go`
- Modify: `docs/json.md` (`containerfile list --json`)

**Interfaces:**
- Consumes: `workspace.Load`, `pack.Catalog.Resolve` (for `Eject`), `render.Instance.Builds`, the Task 8 Containerfile contract check, `pack.ContentHash`
- Produces:

  ```go
  // config/edit.go: line-level edits of the [image] table only; every other byte of project.toml is kept.
  // Each writes temp + rename, after re-parsing the result with Load + Validate.
  // Refuses, saying "edit [image] in project.toml by hand", when [image] is an inline table or a dotted key (image.base = …).
  func SetImageBase(path, value string) error    // adds [image] and/or base; same value = no-op; a different existing value = error naming it
  func AppendImageAddon(path, value string) error // adds [image] and/or addons; appends, order kept; an existing entry = error

  // cli/containerfile.go; Eject is also called by railswitch new (Task 20), before any stack.lock exists, so it takes no Workspace.
  // root = workspace root; st = the stack resolved from the in-memory Project (Catalog.Resolve).
  func Eject(root string, cat *pack.Catalog, st *pack.Stack, name string, force bool) error
  ```

  CLI (D32):
  - `railswitch containerfile list`: one line per `Builds` step of the current worktree: name, file, tag. Then the warnings from `Model.Warnings`. With `--json`: `{"steps":[{"name","file","tag","from"}],"warnings":[…]}`.
  - `railswitch containerfile show <name> [--default]`: prints the workspace file. With `--default`, or when no workspace file exists, it prints the built-in default instead. `<name>` is `base` (the pack's), an option addon (`pg-client`), a tool (`nvim`), or an option's standalone build (`playwright`).
  - `railswitch containerfile eject <name> [--force]` writes `containerfiles/Containerfile.<name>`:
    - the header `# Ejected from <package ref> Containerfile.<name> (sha256 <hex of the default>) by railswitch <Version>. Edit freely: railswitch builds this file.`, then the default's bytes;
    - for `base`, it then calls `SetImageBase`; for an option addon, `AppendImageAddon`; for a tool or a standalone option build (`playwright`, D31), nothing (both are found by filename);
    - an option addon whose `os` differs from the pack's `[image] os` is refused, naming both;
    - an existing file is refused unless `--force`.
  - `railswitch containerfile new <name>`:
    - `<name>` must match `^[a-z][a-z0-9-]{0,30}$` and not name a built-in (`base`, an option addon, a tool, a standalone option build);
    - writes `containerfiles/Containerfile.<name>`:

      ```dockerfile
      # Addon for <project name>. railswitch builds it on the previous image in the chain (see: railswitch containerfile list).
      ARG BASE_IMAGE
      FROM ${BASE_IMAGE}
      USER root
      # RUN apt-get update && apt-get install -y --no-install-recommends <packages> && rm -rf /var/lib/apt/lists/*
      USER appuser
      ```

    - then calls `AppendImageAddon`;
    - refuses if the file exists.

- [ ] **Step 1: Write failing tests.**
  - `TestSetImageBaseKeepsComments`: a commented `project.toml` with no `[image]` gains `[image]\nbase = "containerfiles/Containerfile.base"` at the end; every other line is byte-identical.
  - `TestAppendImageAddonOrder`: `addons = ["containerfiles/Containerfile.a"]` plus `b` gives `addons = ["containerfiles/Containerfile.a", "containerfiles/Containerfile.b"]`.
  - `TestAppendImageAddonDuplicate`: appending `a` again is an error.
  - `TestEditRefusesInline`: `image = { base = "x" }` gives an error containing `by hand`.
  - `TestSetImageBaseOtherValue`: an existing `base = "containerfiles/Containerfile.mine"` and a set to `.base` gives an error naming `Containerfile.mine`.
  - `TestEjectBase`:
    - the file is the header plus the rails default's exact bytes, and `[image] base` is set;
    - after editing the file, `containerfile list` shows a new base tag;
    - a second `eject base` exits 1, and `--force` restores the default.
  - `TestEjectOptionAddon`: `eject pg-client` writes the file and appends it to `[image] addons`, and the `TestOptionAddonWarning` warning disappears. With a test pack declaring `os = "alpine"`, it exits 1 naming `alpine` and `debian`.
  - `TestEjectTool`: `eject nvim` writes `containerfiles/Containerfile.nvim` and leaves `project.toml` unchanged.
  - `TestEjectStandalone`: `eject playwright` writes `containerfiles/Containerfile.playwright` (header + the playwright option's default bytes, whose `FROM` is `mcr.microsoft.com/playwright:v${PLAYWRIGHT_VERSION}-noble`) and leaves `project.toml` unchanged; `list` then shows a `playwright` step with tag `localhost/filial/playwright:1.54.0-<hash8>`.
  - `TestEjectNoWorkspace`: `Eject(root, cat, st, "base", false)` on a root with a `project.toml` that has no `[image]` and no `stack.lock` succeeds and sets `[image] base`.
  - `TestNewAddon`:
    - the skeleton passes the Task 8 contract check;
    - `list` shows it after `addon pg-client`;
    - `new base`, `new nvim`, `new playwright` and `new Bad_Name` exit 2.
  - `TestShowDefault`: after editing `containerfiles/Containerfile.base`, `show base` prints the edited file and `show base --default` prints the built-in.
  - `TestBuiltinContainerfilesSelfContained`: no built-in `Containerfile.*` (base, addons, tools, standalone option builds) has a `COPY` or `ADD` that reads the build context (only `COPY --from=` is allowed). An ejected default must build unchanged with `containerfiles/` as its context.
- [ ] **Step 2:** Run `go test ./internal/config/ ./internal/cli/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(cli): containerfile list/show/eject/new"`.

### Task 13: Stage 1 acceptance: Quadlet dry-run and equivalence review

**Files:**
- Create:
  - `internal/render/quadlet_integration_test.go` (`//go:build integration`)
  - `testdata/legacy/quadlet/*` (copied from `fd7336b` `.container-config/quadlet/`)
  - `docs/stage1-equivalence.md`
- Modify: `.github/workflows/ci.yml` (adds the `quadlet` job, D41)

- [ ] **Step 1: Write the integration test** `TestQuadletDryRun`. Render the Filial fixture into a temp dir. Run `/usr/libexec/podman/quadlet -dryrun -user` with `QUADLET_UNIT_DIRS=<dir>`. Expect exit 0. In the generated `filial-feat-rails.service`, expect `--hooks-dir=`, `--health-cmd` (the app's `HealthCmd=curl -fsS http://localhost:3000/up`, D34), `--sdnotify=healthy`, and `Requires=` naming `filial-feat-dev-network.service` (proves D27). In `filial-feat-deps.service`, expect no `--health-cmd`.
- [ ] **Step 2:** Run `go test -tags integration ./internal/render/ -run TestQuadletDryRun`. Expected: PASS. (It needs podman ≥ 5 on the host.)
- [ ] **Step 2a: Add the CI job.** In `ci.yml`, job `quadlet` (alongside `check`): `runs-on: ubuntu-latest`, `container: quay.io/podman/stable`, steps `actions/checkout`, `actions/setup-go` (Go version from `go.mod`; the image has no Go), then `go test -tags integration ./internal/render/ -run TestQuadletDryRun`.
- [ ] **Step 3: Write `docs/stage1-equivalence.md`.** Generate it with `diff -ru testdata/legacy/quadlet <rendered>`, then annotate every hunk as *same behaviour*, *intended change (cite D#)* or *gap → Stage N*. Expected intended changes:
  - `rustfs-init@` gone (D18);
  - one unit per worktree and service instead of `@` templates, with values written in (D8);
  - split env files (D10);
  - `Notify=healthy` added (the old rails@ comment relies on it, but no old unit sets it);
  - `traefik.docker.network` labels;
  - `<prefix>-proxy.network` subnet from the registry;
  - volume `db-data-pg16` instead of `db-data`;
  - no traefik/dozzle units (Stage 2/3).
- [ ] **Step 4: Checkpoint (user).** Present `docs/stage1-equivalence.md` to the user. Stage 1 is done only when they accept it. Record their decisions at the top of the doc.
- [ ] **Step 5:** `make check && git add -A && git commit -m "test: Stage 1 quadlet dry-run and equivalence review"`.

---

## Stage 2 — Lifecycle CLI

### Task 14: podman and systemctl wrappers

**Files:**
- Create: `internal/podman/podman.go`, `internal/podman/systemd.go`, `internal/podman/podman_test.go`

**Interfaces:**
- Consumes: `sys.Runner`
- Produces (every function takes `ctx` first and returns `error` last; exact argv in the comments):

  ```go
  type Podman struct{ R sys.Runner }
  func (p Podman) ImageExists(ctx, ref) (bool, error)          // podman image exists <ref>  (exit 1 = false)
  func (p Podman) Pull(ctx, ref) (digest string, err error)     // podman pull -q <ref>; podman image inspect --format {{.Digest}} <ref>
  func (p Podman) Build(ctx, b BuildArgs) error                 // podman build --pull=never -f <file> -t <tag> --build-arg K=V… <context dir>  (Timeout 30m); Up prepares the pinned FROM images first (D36)
  func (p Podman) Tag(ctx, src, dst string) error               // podman tag <src> <dst>
  func (p Podman) VolumeExists(ctx, name) (bool, error)         // podman volume exists <name>
  func (p Podman) VolumeCreate(ctx, name) error                 // podman volume create <name>
  func (p Podman) VolumeImport(ctx, name, tarball) error        // podman volume import <name> <tarball>
  func (p Podman) RemoveContainer(ctx, name) error              // podman rm -f --ignore <name>
  func (p Podman) RemoveVolume(ctx, name) error                 // podman volume rm -f <name>; "no such volume" = nil
  func (p Podman) RemoveNetwork(ctx, name) error                // podman network rm -f <name>; "unknown network" = nil
  func (p Podman) Running(ctx, name) (bool, error)              // podman container inspect --format {{.State.Running}} <name>; missing = false
  func (p Podman) GraphRoot(ctx) (string, error)                // podman info --format {{.Store.GraphRoot}}
  func (p Podman) ImageDigest(ctx, ref) (string, error)         // podman image inspect --format {{.Digest}} <ref>; missing image = "", nil
  func (p Podman) Health(ctx, name) (string, error)             // podman container inspect --format {{.State.Health.Status}} <name>; missing container or no healthcheck = "", nil
  type Systemd struct{ R sys.Runner }
  func (s Systemd) DaemonReload(ctx) error                      // systemctl --user daemon-reload
  func (s Systemd) Start/Stop/Restart(ctx, units ...string) error // systemctl --user start|stop|restart <units…>  (Timeout 10m for start)
  func (s Systemd) Show(ctx, unit string, props ...string) (map[string]string, error) // systemctl --user show -p A,B <unit>
  func (s Systemd) ResetFailed(ctx, units ...string) error
  ```

- [ ] **Step 1: Write failing tests** (Fake): every function's argv equals the commented argv. `TestNoFilters` calls every remover with `"api"` and asserts that no recorded argv contains `--filter` and that each argv's last element is exactly `"api"` (Review Focus 2). `ImageExists` maps exit 1 to `false, nil`. `ImageDigest` returns the trimmed stdout (`sha256:aa`), and `"", nil` when inspect fails with `no such image`. `Health` returns `healthy` from stdout, and `"", nil` for a missing container.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(podman): exact-name podman/systemctl wrappers"`.

### Task 15: Shared proxy unit and project network

**Files:**
- Create: `internal/machine/proxy.go`, `internal/machine/proxy_test.go`, `internal/machine/testdata/golden/railswitch-traefik.container`
- Modify: `internal/workspace/workspace.go` (`Render` adds `ProxyUnits` to the Plan's units)

**Interfaces:**
- Consumes: `Registry`, `render.Marker`
- Produces: `func ProxyUnits(r *Registry) map[string][]byte`. It returns `railswitch-traefik.container`:
  - `Image=docker.io/traefik:v3.7`, `ContainerName=railswitch-traefik`;
  - for each project, sorted by subnet: `Network=<prefix>-proxy.network:ip=<TraefikIP>`, `Requires=`/`After=<prefix>-proxy-network.service`;
  - `PublishPort=127.0.0.1:80:80` and `127.0.0.1:8080:8080`;
  - `Volume=%t/podman/podman.sock:/var/run/docker.sock:ro`;
  - `Exec=` the `fd7336b` traefik flags, minus `--providers.docker.network` (containers carry `traefik.docker.network`);
  - `Restart=on-failure`; no `[Install]`.

  The marker says `(machine)`. Orphan removal treats the `machine` prefix the same way.

- [ ] **Step 1: Write failing tests.** Golden with two projects. Zero projects means no file is produced. Lint (Task 10) of the output is clean.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.** In `render`, a changed traefik unit is reported in the Diff; `up` restarts Traefik only when it changed (D21).
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(machine): shared Traefik unit"`.

### Task 16: Firewall: descriptor, embedded hook, `fw show`/`fw allow`

**Files:**
- Create:
  - `internal/firewall/descriptor.go`, `firewall_test.go`
  - `internal/firewall/assets/`: `hook.sh`, `firewall.sh` (ported from `fd7336b` `.container-config/firewall/hook.sh` and `.scripts/firewall.sh`). The old `agent.allow` is not ported: its hosts are Claude's and belong to the `claude@1` agent template (Stage 4)
  - `internal/firewall/testdata/netavark/filial-feat-dev.json`
- Modify: `internal/workspace/workspace.go` (`Render` merges `firewall.Files` into `Plan.Files`; `firewall` imports `render` for `render.File`, never the reverse), `internal/cli/` (`fw` group)

**Interfaces:**
- Consumes: `Podman.GraphRoot`, `pack.Stack` (deps registries), `config.Firewall.Allow`
- Produces:
  - `func Files(prefix, ws, networkDir string, st *pack.Stack, fw config.Firewall) (map[string]render.File, error)`. It returns:
    - `.railswitch/firewall/hook.sh` (0755) and `.railswitch/firewall/firewall.sh` (0755);
    - `.railswitch/firewall/deps.allow`: the pack's `deps.allow`, then the options' registries, then `Allow["deps"]`, de-duplicated, order kept;
    - `.railswitch/firewall/agent.allow`: `Allow["agent"]` only. From Stage 4 the chosen agent package's `allow` comes first;
    - `.railswitch/firewall/hooks.d/<prefix>-firewall.json`: `{"version":"1.0.0","hook":{"path":"<ws>/.railswitch/firewall/hook.sh","args":["hook.sh","<prefix>","<ws>/.railswitch/firewall"],"env":["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin","NETWORK_DIR=<networkDir>"],"timeout":60},"when":{"annotations":{"^<prefix>\\.firewall$":".*"}},"stages":["createRuntime"]}`.
  - The `hook.sh` port changes only how `ROOT` is found: `ROOT` is now argv 2 (state at `$ROOT/state`, log at `$ROOT/hook.log`). The rest is unchanged.
  - CLI:
    - `railswitch fw show [svc]` runs `firewall.sh show` via the Runner, with `FIREWALL_DIR`/`PREFIX` env;
    - `railswitch fw allow <svc> <host…>` is the same for `allow`.

- [ ] **Step 1: Write failing tests.**
  - The descriptor JSON matches the golden.
  - deps.allow = pack + extra `github.com`, with no duplicates.
  - `TestHookReadsNetavarkSubnet` (gotcha: subnets come from netavark json, not the netns):
    1. Put shims on `PATH`: `nsenter` runs its remaining args after `-n`, and `nft` copies stdin to `$OUT`.
    2. Feed `hook.sh filial <tmp>` OCI state JSON on stdin, with annotations `filial.firewall=none`, `filial.firewall.mode=enforce`, `filial.firewall.networks=filial-feat-dev`, and `NETWORK_DIR=testdata/netavark`.
    3. Assert that `$OUT` contains the fixture subnet `10.89.7.0/24`.
  - `TestHookFailsClosed`: a missing networks annotation exits non-zero.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(firewall): descriptor, ported hook, fw show/allow"`.

### Task 16A: Secrets: `secret add|set|ls|rm` and delivery (D33)

**Files:**
- Create:
  - `internal/cli/secret.go`, `internal/cli/secret_test.go`
  - `internal/cli/noecho_linux.go` (the no-echo prompt: `syscall` `TCGETS`/`TCSETS` ioctls)
  - `internal/machine/config.go`, `internal/machine/config_test.go`
  - `internal/lifecycle/preflight.go`, `internal/lifecycle/preflight_test.go` (`CheckSecrets`; Task 18 adds the rest of `Preflight` to this file)
- Modify:
  - `internal/podman/podman.go` + test;
  - `internal/render/quadlet.go` + goldens (`Secret=` lines);
  - `internal/config/edit.go` + `edit_test.go` (`AddSecret`);
  - `testdata/fixtures/filial/project.toml`: add `[secrets.github_token]` with `services = ["nvim"]`;
  - `docs/json.md` (`secret ls --json`).

**Interfaces:**
- Consumes: `config.Secret`, `Values["secret.<name>"]`, `sys.Runner`, `render.Model.NamesFor` (Task 8), `podman.Podman.Running`, `podman.Systemd.Restart`
- Produces:

  ```go
  func (p Podman) SecretExists(ctx, name) (bool, error)                  // podman secret exists <name>  (exit 1 = false)
  type Driver struct{ Name string; Opts map[string]string }      // from machine config (D37); zero value = podman's default (file)
  func (p Podman) SecretCreate(ctx, name string, value io.Reader, replace bool, d Driver) error // podman secret create [--replace] [--driver=<name> --driver-opts=<k=v,… sorted by key>] <name> -  (value on stdin)
  func (p Podman) SecretDriver(ctx, name) (string, error)        // podman secret inspect --format {{.Spec.Driver.Name}} <name>
  func (p Podman) SecretRemove(ctx, name) error                          // podman secret rm <name>
  func (p Podman) SecretCreated(ctx, name) (time.Time, error)            // podman secret inspect --format {{.CreatedAt}} <name>

  // config/edit.go: line-level append of one [secrets.<name>] table (services, env, scope as given; empty fields omitted);
  // every other byte kept; temp + rename after Load + Validate; an existing name is an error.
  func AddSecret(path, name string, s Secret) error

  // machine/config.go
  type Config struct {
      Schema  int           `toml:"schema"`  // 1 (D39); a higher value is refused ("written by a newer railswitch")
      Secrets SecretsConfig `toml:"secrets"`
  }
  type SecretsConfig struct {
      Driver     string            `toml:"driver"`      // "" = podman's default (file)
      DriverOpts map[string]string `toml:"driver_opts"`
  }
  func LoadConfig(d Dirs) (*Config, error) // <d.Config>/config.toml; strict decode; a missing file = zero Config, no error

  // lifecycle/preflight.go
  func CheckSecrets(ctx context.Context, p podman.Podman, m *render.Model, slug string) error
  // for each secret a service of this worktree receives: SecretExists; missing → "secret <name> is not set: railswitch secret set <name>"
  ```

  - **Units:** each service in a secret's `services` gets one line. File delivery (default): `Secret=<podman name>,type=mount,target=/run/secrets/<name>,uid=1000,gid=1000,mode=0400`. With `env` set: `Secret=<podman name>,type=env,target=<ENV>`, e.g. `Secret=filial_twilio_auth_token,type=env,target=TWILIO_AUTH_TOKEN` in `filial-<slug>-rails.container`. The transient `exec` container (Task 18) gets the same as `--secret` flags. So `railswitch exec -- bin/rails console` sees the same env as the running app.
  - **Preflight (`up`, `exec`):** for each secret a service of this worktree receives, `SecretExists`. If it's missing, refuse with `secret <name> is not set: railswitch secret set <name>`.
  - **`railswitch secret set <name> [--restart]`:**
    - `<name>` must be declared in `[secrets]`;
    - the value is read from stdin; on a TTY, prompt `Value for <name>:` with no echo (stdlib `syscall` `TCGETS`/`TCSETS` ioctls on stdin's fd: clear `ECHO`, read one line, restore the saved termios, also on error);
    - one trailing newline is stripped; an empty value or a value with an inner newline is refused;
    - then `SecretCreate(name, value, replace = SecretExists(name))`. podman's `--replace` fails on a missing secret ([#29612](https://github.com/podman-container-tools/podman/issues/29612)), hence the check;
    - podman's `--replace` changes only newly created containers, so `set` then lists the running containers that receive the secret (exact names via `NamesFor`, `Running`) and says they keep the old value until restarted. With `--restart`, it restarts them.
  - **`railswitch secret add <name> --service <id>… [--env <VAR>] [--machine]`:** declares `[secrets.<name>]` in `project.toml` via `config.AddSecret` (keeping comments; refuses an existing name), then runs `set`. `railswitch secret add twilio_auth_token --service rails --service worker --env TWILIO_AUTH_TOKEN` is the whole workflow for an app key.
  - **`railswitch secret ls`:** one line per declared secret: name, scope, podman secret name, delivery (`file` or `env VAR`), driver, `set <created>` or `not set`, receiving services. Never a value. A secret stored with a driver other than the configured one is flagged `driver differs: railswitch secret set <name> to move it`. With `--json`, the same fields.
  - **`railswitch secret rm <name>`:** `SecretRemove`. The `project.toml` declaration stays until the user deletes it, and `up` then refuses with the `secret set` hint.
  - **`railswitch new`** (Task 20) writes `[secrets.github_token]` with `services = ["nvim"]` and `git_host = "github.com"` when the repo URL's host is `github.com` and nvim is enabled (`--tool nvim`). It then prints `railswitch secret set github_token` as the next step.
  - **Secret driver (D37):** machine-wide, in `~/.config/railswitch/config.toml` (`machine.LoadConfig`, above):

    ```toml
    schema = 1
    [secrets]
    driver      = "pass"   # default "file"
    driver_opts = { key = "<GPG key id>", root = "~/.local/share/railswitch/pass" }
    ```

    - `pass` stores each value GPG-encrypted, using the `pass` tool's store.
    - podman decrypts the value when it creates a container, so gpg-agent must hold the key unlocked when units start. If it doesn't, `up` fails, and its error names gpg-agent.
    - Switching drivers doesn't move existing values; railswitch never reads them. `secret ls` flags them, and `secret set` re-creates each under the new driver.

- [ ] **Step 1: Write failing tests.**
  - `TestSecretSetStdin`: `secret set github_token` with stdin `tok123\n`:
    - the Fake records `podman secret exists filial_github_token`, then `podman secret create filial_github_token -` with stdin exactly `tok123`;
    - no recorded argv and no stdout/stderr byte contains `tok123`.
  - `TestSecretSetReplace`: when `exists` succeeds, the create argv has `--replace`.
  - `TestSecretSetRejects`: an undeclared name exits 2; empty stdin exits 1; `a\nb` exits 1.
  - `TestSecretSetRunningNotice`: with `filial-master-nvim` running, the output names it and says `restart`. With `--restart`, the Fake records `systemctl --user restart filial-master-nvim.service`.
  - `TestSecretLsNoValues`: output has `github_token`, `project`, `filial_github_token`, `not set`, `nvim`.
  - `TestMachineScope`: `scope = "machine"` uses `railswitch_github_token` everywhere.
  - `TestSecretDriverPass`: with config `driver = "pass"`, `driver_opts = { root = "/x", key = "ABC" }`, the create argv is `podman secret create --driver=pass --driver-opts=key=ABC,root=/x filial_github_token -`. With no config, the argv has no `--driver`.
  - `TestSecretLsDriverDiffers`: the configured driver is `pass`, but `SecretDriver` returns `file`; the output contains `driver differs`.
  - `TestUnitSecretLines`: the golden `filial-master-nvim.container` has the mount `Secret=` line; `filial-master-rails.container` has none; with `env = "GH_TOKEN"` the line is `type=env,target=GH_TOKEN`.
  - `TestEnvSecretForApp`: `[secrets.twilio_auth_token] services = ["rails"]`, `env = "TWILIO_AUTH_TOKEN"`:
    - `filial-master-rails.container` has `Secret=filial_twilio_auth_token,type=env,target=TWILIO_AUTH_TOKEN`;
    - `filial-master-deps.container` and `filial-master-nvim.container` have no line for it;
    - the transient `exec` argv has `--secret filial_twilio_auth_token,type=env,target=TWILIO_AUTH_TOKEN`.
  - `TestSecretAdd` (on a fixture copy with `[secrets.twilio_auth_token]` removed): `secret add twilio_auth_token --service rails --env TWILIO_AUTH_TOKEN` with stdin `AC123\n`:
    - `project.toml` gains the `[secrets.twilio_auth_token]` table, with other lines byte-identical;
    - the Fake records `podman secret create filial_twilio_auth_token -` with stdin `AC123`;
    - a second `add` with the same name exits 1;
    - `--service nope` exits 2, naming the known services.
  - `TestPreflightMissingSecret` (`lifecycle.CheckSecrets`, Fake `secret exists filial_github_token` exit 1): the error contains `railswitch secret set github_token`.
  - `TestAddSecretKeepsComments` (`config.AddSecret`): a commented `project.toml` gains `[secrets.x]\nservices = ["rails"]` at the end; every other line is byte-identical; adding `x` again is an error.
  - `TestLoadConfig`: a missing `config.toml` gives a zero `Config` and no error; `driver = "pass"` with `driver_opts` parses; `schema = 2` gives an error containing `newer railswitch`; an unknown key is an error.
- [ ] **Step 2:** Run `go test ./internal/podman/ ./internal/cli/ ./internal/render/ ./internal/lifecycle/`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat: project secrets via podman secrets"`.

### Task 17: Worktrees: `wt add`, `wt rm`, `wt ls`

**Files:**
- Create: `internal/worktree/add.go`, `remove.go`, `resolve.go`, `worktree_test.go`
- Modify: `internal/cli/` (`wt` group; wires `Manager.Render`, `Manager.Names` and `Manager.WorktreeVolumes`), `docs/json.md` (`wt ls --json`)

**Interfaces:**
- Consumes: `sys.Runner` (git real, gh/podman/systemctl faked in tests), `machine.Open`, `naming.Sanitize`, `render.Model.NamesFor` (Task 8, via `Manager.Names`), `safefile.Read`, `podman.Podman`, `podman.Systemd`
- Produces:

  ```go
  type Manager struct {
      R   sys.Runner
      WS  string
      Pfx string
      Data string // machine.Dirs.Data. Open the registry only around each registry change (Open → change → Save → Close), never across Render: Render opens it too, and flock is per open file description, so holding it would deadlock
      In  io.Reader
      Out io.Writer
      TTY bool
      Render          func(ctx context.Context) error                // wired to workspace.Render by the CLI
      Names           func(slug string) (render.Names, error)        // wired by the CLI: build the Model, then Model.NamesFor(slug)
      WorktreeVolumes []string                                       // mounts of the pack's [[worktree_volumes]] relative to /app (e.g. "node_modules"), wired by the CLI from the Stack
  }
  type Row struct{ Slug string; ID int; Branch, Dir string; Dirty bool }
  func (m *Manager) Add(ctx, target string, name string) (render.Worktree, error)
  // target = branch | PR number (gh pr view <n> --json headRefName -q .headRefName) | new branch from base HEAD.
  // Steps:
  //   1. name: --name, else a prompt `Worktree name [<default>]:` when TTY, else Sanitize(branch) with "-<4 hex of sha256(branch)>" when taken;
  //   2. git -C <base> worktree add <ws>/<slug> <branch> (or -b);
  //   3. only then machine.Open(Data) → AddWorktree → Save → Close;
  //   4. pre-create <ws>/<slug>/<p> for each p in m.WorktreeVolumes (so the worktree volume mounts over a dir the user owns);
  //   5. for each Meta.SeedFiles path present in base and absent in the new worktree: safefile.Read(<base>/<path>, safefile.Limit);
  //      a refused/oversized file is skipped with a warning on Out naming it; otherwise write it at mode 0600;
  //   6. m.Render(ctx).
  func (m *Manager) Remove(ctx, target string, force bool) error
  // Resolve target: absolute path → branch checked out in a worktree → exact worktree name.
  // Refused: target hits two worktrees; the base worktree; a dirty worktree (git status --porcelain) unless force.
  // Abort before any change if any registered worktree dir of this project is missing (partial view).
  // Then, with names, err := m.Names(slug):
  //   1. Systemd.Stop(names.Units...);
  //   2. RemoveContainer / RemoveVolume (per-worktree volumes only) / RemoveNetwork, by exact name;
  //   3. rm -rf .railswitch/env/<slug>;
  //   4. machine.Open(Data) → RemoveWorktree → Save → Close;
  //   5. git -C <base> worktree remove --force <path>;
  //   6. m.Render(ctx): deletes the slug's unit files (D28); then Systemd.DaemonReload.
  func (m *Manager) List(ctx) ([]Row, error)                          // `wt ls`; `wt ls --json` prints the rows as [{"slug","id","branch","dir","dirty"}]
  ```

- [ ] **Step 1: Write failing tests.** Use real git: a temp bare remote plus the base clone. Fake the rest.
  - `TestAddAllocatesAfterGit`: Add `feat` gives ID 2 (the base is 1), and the registry has the entry.
  - `TestAddSeedFiles`: with `seed_files = [".env", "config/master.key"]`, `.env` is copied at mode 0600; a `config/master.key` that is a symlink in base is not copied, and `Out` has a warning naming it.
  - `TestAddPrecreatesWorktreeVolumes`: with `WorktreeVolumes = ["node_modules"]`, `<ws>/feat/node_modules` exists after Add.
  - `TestAddGitFailsNoRegistry` (Review Focus 4): Add a branch already checked out in base fails, the registry is unchanged, and no env dir exists.
  - `TestAddNameCollision`: a second worktree for branch `feat` gets slug `feat-<4hex>`.
  - `TestRemovePrecedence`: a path beats a branch, which beats a name.
  - `TestRemoveAmbiguous`: a branch `x` checked out in worktree `y` while another worktree is named `x` is refused.
  - `TestRemoveBaseRefused`.
  - `TestRemoveDirtyRefused`, and the same with `force=true` succeeding.
  - `TestRemovePartialViewAborts`: rename another worktree's dir; Remove errors and no podman/git calls were made.
  - `TestRemoveExactNames` (Review Focus 2): worktrees `api` and `api-v2`; Remove `api`; every Fake argv names only `…-api-…` objects; `.railswitch/env/api-v2` still exists.
  - `TestNeverPrune`: no recorded argv contains `prune`.
  - `TestListJSON`: `wt ls --json` parses into `[{"slug","id","branch","dir","dirty"}]` with the base at ID 1.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.** Every git call passes `-c core.fsmonitor=false -c core.hooksPath=/dev/null`.
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(worktree): scoped add/remove with exact-name teardown"`.

### Task 18: Lifecycle: `up`, `stop`, `down`, `restart`, `status`, `logs`, `exec`, `build`

**Files:**
- Create: `internal/lifecycle/{up,stop,down,status,logs,exec,build,deps}.go`, `internal/lifecycle/lifecycle_test.go`
- Modify:
  - `internal/lifecycle/preflight.go` + `preflight_test.go` (created in Task 16A with `CheckSecrets`; this task adds `Preflight`);
  - `internal/cli/` (commands);
  - `docs/json.md` (`status --json`).

**Interfaces:**
- Consumes: Tasks 8–17; in particular `render.Model.NamesFor` (returns `(Names, error)`), `lifecycle.CheckSecrets` (Task 16A), `config.Lock.SetDigest`/`SetBaseDigest` (Task 7), `podman.Podman.ImageDigest`/`Health` (Task 14), `sys.NoTimeout` (Task 1)
- Produces (`type L struct{ R sys.Runner; WS *workspace.Workspace; Out, Err io.Writer; In io.Reader; TTY bool }`; `func (l *L) Preflight(ctx context.Context, m *render.Model, slug string) error` in `preflight.go`). Each step:
  - **`Up(ctx, slug)`**, in this order, stopping at the first error:
    1. `WS.Render(ctx, R, false)`, which includes the lock check;
    2. `Preflight`:
       - every absolute bind-mount source exists, naming the missing path;
       - `hooks.d/<prefix>-firewall.json` exists, else refuse: `containers would start unfirewalled`;
       - the worktree dir exists;
       - every secret this worktree's services receive exists (`CheckSecrets`, Task 16A);
    3. for each pulled image with no lock digest: `Pull`, `Lock.SetDigest(ref, digest)`, `Lock.Write`, then render again so the units carry `ref@digest`;
    4. **pinned base images (D36):** for each distinct ref in the `From` of every `Instance.Builds` step:
       - with no `Lock.BaseDigest(ref)`: `Pull(ref)`, then `SetBaseDigest` and `Lock.Write`;
       - with a recorded digest, when `ref` is missing locally (`ImageDigest` returns `""`) or `ImageDigest(ref)` differs from it: `podman pull <repo>@<digest>`, then `Tag(<repo>@<digest>, ref)`.

       Then, for each `Instance.Builds` step in order (base chain, tools, then standalone builds such as `playwright`) where `!ImageExists(step.Tag)`: `Build` (`--pull=never`, so the build uses exactly the pinned FROM images). Steps run strictly in order, so every image exists before anything built on it;
    5. for each option volume with a `seed` where `!VolumeExists`: `VolumeCreate`, then `VolumeImport` if `<ws>/db-dumps/<seed>` exists;
    6. if the Diff has unit changes: `DaemonReload`, and `Restart railswitch-traefik.service` if its unit changed;
    7. `Start <prefix>-<slug>-<app>.service`.
  - **`Stop(ctx, slug)`:** stops every unit of the slug (`names, err := Model.NamesFor(slug)`, then `Stop(names.Units...)`).
  - **`Restart(ctx, slug)`:** Stop, then Up.
  - **`Down(ctx, slug, yes bool)`:**
    - it prompts `Delete <N> volumes of <slug>? [y/N]`, defaulting to No, unless `yes`; a non-TTY without `--yes` refuses;
    - it then runs Stop, `RemoveContainer` for each container, and `RemoveVolume` for the per-worktree volumes;
    - shared `_` volumes and images are never touched.
  - **`Status(ctx, slug)`:** a table of `SERVICE ROLE UNIT-STATE HEALTH`, from `Show ActiveState,SubState`, plus `Podman.Health(<container>)` for services with health (the app included, D34). With `--json`, the same fields.
  - **`Logs(ctx, slug, svc, follow bool)`:**
    - oneshot/deps: `journalctl --user -u <unit> [-f] --no-pager`;
    - otherwise: `podman logs [-f] <container>`;
    - streamed to `Out`; when following, `Timeout: sys.NoTimeout` (ends with the ctx, e.g. Ctrl-C); otherwise the default.
  - **`Exec(ctx, slug, fresh bool, argv []string)`:**
    - if not `fresh` and the app container is `Running`: `podman exec [-t] -i -w /app <ctr> argv…`;
    - else a transient `podman --hooks-dir <…> run --rm -i [-t] --label traefik.enable=false --annotation <same firewall annotations> --network <dev> --env-file <app.env> -v …(app mounts) -w /app <IMAGE> argv…`, built from the same `Service` model (not by parsing units);
    - `-t` only when `TTY`; both forms run with `Timeout: sys.NoTimeout` (an interactive `bin/rails console` has no deadline);
    - refuses without the hook descriptor.
  - **`Build(ctx, slug, force, pull bool)`:** builds every built image of the slug; `force` rebuilds existing tags. `pull` (`railswitch build --pull`, D36):
    - re-pulls every FROM ref by tag, records the new digests in `stack.lock`, and prints `<ref>: <old> → <new>` for each that changed;
    - rebuilds every step from the first one whose FROM digest changed. That's the only way an upstream base image update enters a build.
  - **`Deps(ctx, slug, argv)`** (`railswitch deps -- <cmd>`, D35): a transient container built from the deps service model:
    - the base image, the deps mounts, firewall profile `deps`, `-w /app`;
    - env from `Instance.Env["deps"]` minus the pack's `[deps] frozen_env` names, passed via a temporary 0600 env file that is removed afterwards;
    - `Timeout: 30 * time.Minute` (an install can be slow; 30 s would kill it).

    This is how lockfiles change: `railswitch deps -- bundle add rack-attack`, `railswitch deps -- npm install left-pad`.

- [ ] **Step 1: Write failing tests** (Fake + temp workspace):
  - `TestUpOrder`: the recorded argv sequence is:
    1. render;
    2. `podman pull` → `podman image inspect` (service images);
    3. `podman pull` + `podman image inspect` for each base FROM ref (`docker.io/library/node:22.11.0-slim`, `docker.io/library/ruby:3.3.6-slim`, `docker.io/library/debian:trixie-slim` (nvim's build stage), `mcr.microsoft.com/playwright:v1.54.0-noble` (the standalone playwright build));
    4. for each of base, `addon pg-client`, nvim, playwright: `podman image exists` then `podman build --pull=never` (each chain/tool argv after the first contains `--build-arg BASE_IMAGE=` with the previous chain step's tag; the playwright argv has `-f <ws>/containerfiles/Containerfile.playwright`, `-t localhost/filial/playwright:1.54.0-<hash8>`, `--build-arg PLAYWRIGHT_VERSION=1.54.0` and no `BASE_IMAGE`);
    5. `podman volume exists` → `podman volume create` → `podman volume import filial-feat-<rustfs volume> <ws>/db-dumps/rustfs_data.tar.gz` (the Task 8 fixture ships that file);
    6. `systemctl --user daemon-reload` → `systemctl --user start filial-feat-rails.service`.
  - `TestUpRecordsDigest`: after Up, `stack.lock` has the digest and `filial-feat-db.container` has `Image=…@sha256:…`.
  - `TestUpPinsBaseImages`:
    - the first Up runs `podman pull docker.io/library/ruby:3.3.6-slim` and records its digest under `[[base_image]]`;
    - a second Up, with that image missing (Fake: `image exists` exit 1), runs `podman pull docker.io/library/ruby@sha256:…` then `podman tag docker.io/library/ruby@sha256:… docker.io/library/ruby:3.3.6-slim`;
    - every `podman build` argv has `--pull=never`.
  - `TestBuildPull`: `Build(pull=true)` with a changed digest prints `docker.io/library/ruby:3.3.6-slim: sha256:aaa → sha256:bbb` and rebuilds base and every later step.
  - `TestTimeouts`: the Fake records each `sys.Cmd`; `logs -f` and both `exec` forms have `Timeout == sys.NoTimeout`; `deps -- bundle add x` has `Timeout == 30*time.Minute`; `logs` without `-f` has `Timeout == 0`.
  - `TestUpLocalDigestDiffers`: with a recorded base digest `sha256:aa` and `ImageDigest` returning `sha256:zz`, Up runs `podman pull docker.io/library/ruby@sha256:aa` then `podman tag`.
  - `TestDepsUnfrozen`:
    - `deps -- bundle add x` runs with argv containing `--annotation filial.firewall=deps` and `-w /app`, and its env file has no `BUNDLE_FROZEN`;
    - `filial-master-deps.container`'s env file has `BUNDLE_FROZEN=true`;
    - the temporary env file is gone afterwards.
  - `TestStatusJSON`: `--json` output parses into `[{"service","role","state","health"}]`; the Fake records `podman container inspect --format {{.State.Health.Status}} filial-feat-rails`.
  - `TestUpSecondRunNoPull`: a second Up makes no pull and no daemon-reload.
  - `TestUpBuildFailsNoStart` (Review Focus 4): `podman build` fails, so there is no `systemctl start` and a re-run retries the build.
  - `TestPreflightMissingBind`: the error names the path.
  - `TestPreflightFreshWorkspace`: with the fixture's `db-dumps/` and the rendered `.railswitch/assets/`, Preflight passes (every bind source exists).
  - `TestPreflightNoDescriptor`: the error contains `unfirewalled`.
  - `TestDownDefaultsNo`: input `\n` means no remove calls.
  - `TestDownExactNames`: with slugs `api` and `api-v2`, only `api` objects are removed (Review Focus 2).
  - `TestDownKeepsShared`: no argv contains `filial_`.
  - `TestLogsDepsUsesJournal`.
  - `TestExecRunningUsesExec`.
  - `TestExecTransientLabels`: the argv contains `--label traefik.enable=false` and `--hooks-dir`.
  - `TestExecNoTTYNoT`.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(lifecycle): up/stop/down/restart/status/logs/exec/build"`.

### Task 19: `doctor` and `allow-ports`

**Files:**
- Create: `internal/doctor/doctor.go`, `internal/doctor/doctor_test.go`
- Modify: `internal/cli/`, `docs/json.md` (`doctor --json`)

**Interfaces:**
- Produces:
  - `type Check struct{ Name string; OK bool; Detail, Fix string }`
  - `func Run(ctx context.Context, r sys.Runner, ws *workspace.Workspace) []Check`
  - `railswitch doctor` exits 1 if any check fails. With `--json`: `[{"name","ok","detail","fix"}]`.
  - `railswitch allow-ports` prints and runs `sudo sysctl -w net.ipv4.ip_unprivileged_port_start=80`, and writes `/etc/sysctl.d/90-railswitch.conf` via `sudo tee`, after a `[y/N]` prompt.
- Checks, by Name:
  - `podman >= 5.0` (`podman version --format {{.Client.Version}}`);
  - `quadlet generator` (`/usr/libexec/podman/quadlet` exists);
  - `podman.socket active` (`systemctl --user is-active podman.socket`);
  - `unprivileged port 80` (read `/proc/sys/net/ipv4/ip_unprivileged_port_start` ≤ 80);
  - `nft`, `nsenter`, `getent`, `jq`, `curl` on PATH (`exec.LookPath`);
  - `firewall descriptor` present;
  - `units current` (render check-mode Diff empty);
  - `cgroup v2` (`/sys/fs/cgroup/cgroup.controllers` exists);
  - only with the `pass` secret driver (D37): `pass` and `gpg` on PATH, and `gpg --list-secret-keys <key>` succeeds. The fix text names `driver_opts.key`.
- [ ] **Step 1: Write failing tests.** Fake outputs drive every check both OK and failing; the proc/sys paths are injectable via a struct field. Assert the Fix text for port 80 is `railswitch allow-ports`. With no machine config, no `pass` check runs.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(doctor): host checks and allow-ports"`.

### Task 20: `railswitch new` and `railswitch init`

**Files:**
- Create: `internal/cli/new.go`, `internal/cli/new_test.go`

**Interfaces:**
- Consumes: `config`, `pack.Catalog` (+ `Catalog.Resolve` on the in-memory `Project`), `machine.Locked`, `worktree`, `cli.Eject` (Task 12A: `Eject(root string, cat *pack.Catalog, st *pack.Stack, name string, force bool) error`), `workspace.Lock(ctx, root)` (Task 12), `workspace.Render`
- Produces:
  - Usage: `railswitch new <dir> --repo <url> --prefix <p> [--name <n>] [--branch <base>] [--pack rails@1] [--option slot=ref]… [--tool nvim]`.
  - `railswitch init` (D38), run in a cloned workspace repo (it has `project.toml` but isn't registered on this machine): clone `[project] repo` (branch `[project] base_branch`) into the base dir, create `<ws>/db-dumps/` (0700), write `mise.local.toml` with fresh `generated` values, register the prefix and base worktree, and render. Then print `railswitch secret set <name>` for each declared secret that isn't set. Refuse if the prefix is registered.
  - Steps:
    1. refuse if `<dir>/project.toml` exists or the prefix is registered;
    2. resolve `<base>`: `--branch`, else the remote's HEAD branch from `git ls-remote --symref <repo> HEAD` (the line `ref: refs/heads/<base>\tHEAD`); a repo with no symref HEAD is an error asking for `--branch`. `<base>` is recorded as `[project] base_branch`. Then `git clone --branch <base> <repo> <dir>/<sanitize(base)>`. Then make the workspace its own git repo (D38): `git init <dir>` and write `<dir>/.gitignore` as an allowlist, `/*`, `!/.gitignore`, `!/project.toml`, `!/stack.lock`, `!/containerfiles/`, `!/entrypoints/`, `!/packs/`, `!/options/` (the last two for Stage 5 local packs). Create `<dir>/db-dumps/` (0700; postgres mounts it, and `up` preflight needs every bind source). Worktrees, `.railswitch/`, `mise.local.toml` and `db-dumps/` are therefore never tracked. There's no commit; the user commits;
    3. write `project.toml`:
       - `[project]` `name`, `prefix`, `repo`, `base_branch = "<base>"`; pack defaults overlaid by `--option`;
       - `[versions]` from the options' pin defaults;
       - `schema = 1`, and `[tools] nvim = true` with `--tool nvim`;
       - when the repo URL's host is `github.com` and nvim is enabled, `[secrets.github_token]` with `services = ["nvim"]`, `git_host = "github.com"`. The last line `new` prints is then `next: railswitch secret set github_token` (D33);
    4. resolve the stack from the in-memory `Project` (`Catalog.Resolve`), then `Eject(dir, cat, st, name, false)` for every default the stack builds: `base` first (it sets `[image] base`, which `Validate` requires), then each chosen option's `[app_addon]`, each enabled tool, and each chosen option's standalone build (`playwright` when `browser = "playwright@1"`; written to `containerfiles/Containerfile.playwright`). After this, every Containerfile the project builds is a file in `containerfiles/` that the user can edit;
    5. write `mise.local.toml` (0600) with `[env] <NAME> = "<32 hex from crypto/rand>"` for each chosen option's `generated` names (D17);
    6. `workspace.Lock(ctx, dir)` (no `stack.lock` exists yet, so not `workspace.Load`);
    7. `Reg.AddProject` and `Reg.AddWorktree(base)`;
    8. `workspace.Load` then `Render`.
  - If a step fails after the clone, the error lists what exists and the command to remove it. It does not auto-delete the user's clone (HANDOFF §9 transactional create is open).
- [ ] **Step 1: Write failing tests.**
  - With a local bare repo as `--repo`, `new` writes `project.toml` that `config.Load` + `Validate` accept, with `schema = 1`.
  - `mise.local.toml` is mode 0600 and holds `DB_PASSWORD`.
  - Without `--tool nvim`:
    - `containerfiles/` holds `Containerfile.base` and `Containerfile.pg-client`, each starting with the `# Ejected from` header;
    - `project.toml` has `[image] base = "containerfiles/Containerfile.base"` and `addons = ["containerfiles/Containerfile.pg-client"]`, no `[tools]`, no `[secrets]`, and `browser = "none"`.
  - With `--tool nvim` and a `github.com` repo URL: `containerfiles/Containerfile.nvim` exists, and `[secrets.github_token]` is declared.
  - With `--option browser=playwright@1`: `containerfiles/Containerfile.playwright` exists with the `# Ejected from` header, and `project.toml`'s `[image]` is unchanged by it.
  - `railswitch containerfile list` in the new workspace prints no warnings.
  - `<dir>/db-dumps/` exists with mode 0700.
  - `git -C <dir> status --porcelain --untracked-files=all` lists only `.gitignore`, `project.toml`, `stack.lock` and files under `containerfiles/`. Never `mise.local.toml`, `.railswitch/`, `db-dumps/` or `main/`.
  - `TestNewDefaultBranch`: with no `--branch` and a bare repo whose HEAD is `refs/heads/trunk`, the base dir is `<dir>/trunk/` and `project.toml` has `base_branch = "trunk"`.
  - `project.toml` does not contain the password value.
  - The registry has the prefix and base ID.
  - A second `new` with the same prefix fails with `prefix "x" is already used by <workspace>`.
  - `TestInitJoin` (D38): `railswitch init`, in a directory holding only the tracked files of a `new` workspace and pointed at a fresh `XDG_DATA_HOME`:
    - clones `[project] repo` into the base dir;
    - writes `mise.local.toml` with fresh `generated` values;
    - registers the prefix and base worktree, creates `db-dumps/` (0700), and renders;
    - prints `railswitch secret set <name>` for each declared secret that isn't set.

    It refuses when the prefix is already registered.
- [ ] **Step 2:** Run it. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4:** Run it. Expected: PASS.
- [ ] **Step 5:** `make check && git commit -am "feat(cli): railswitch new and init"`.

### Task 21: Stage 2 acceptance: a fresh Rails project end to end

**Files:**
- Create: `internal/lifecycle/e2e_integration_test.go` (`//go:build integration`), `docs/stage2-acceptance.md` (the run log)

- [ ] **Step 1: Generate a fresh app without host Ruby:**

  ```bash
  mkdir -p /tmp/rs-demo
  podman run --rm -v /tmp/rs-demo:/w:Z -w /w docker.io/library/ruby:3.4 \
    sh -c 'gem install rails && rails new demo -d postgresql && cd demo && git config --global user.email t@t && git config --global user.name t && git add -A && git commit -qm init'
  ```

  Expected: `/tmp/rs-demo/demo/.git` exists.
- [ ] **Step 2:** Run `railswitch doctor`. Expected: every check OK (run `railswitch allow-ports` first if port 80 fails).
- [ ] **Step 3:** Run `railswitch new ~/workspace/rs-demo --repo /tmp/rs-demo/demo --prefix demo`, then `cd ~/workspace/rs-demo/main && railswitch up`. Expected: exit 0.
- [ ] **Step 4:** Run `curl -sS -o /dev/null -w '%{http_code}' http://main.demo.localhost/`. Expected: `200`. `up` already waited for the `/up` health check (D34). If Rails host authorization or binding blocks it, the fix belongs in the rails pack (`BINDING`), or in the project's `[env]` for an app-specific setting. Never in core.
- [ ] **Step 5:** Run `railswitch exec -- bin/rails runner 'puts ActiveRecord::Base.connection.select_value("select 1")'`. Expected: `1`.
- [ ] **Step 6:** Run `railswitch fw show`. Expected: rails is listed with profile `none` and mode `log`; no container is reported as unfirewalled.
- [ ] **Step 7:** Run `railswitch wt add feat-x && railswitch -w feat-x up && curl -s -o /dev/null -w '%{http_code}' http://feat-x.demo.localhost/`. Expected: `200`, with ports `55434`/`33002`.
- [ ] **Step 8:** Run `railswitch -w feat-x down --yes && railswitch wt rm feat-x`. Expected: `podman ps -a --format {{.Names}}` and `podman volume ls --format {{.Name}}` show nothing named `demo-feat-x-*`; `demo-main-*` is still running.
- [ ] **Step 9:** Encode steps 3–8 in `e2e_integration_test.go`, taking the repo path from `RAILSWITCH_E2E_REPO`; it is skipped when unset. Run `RAILSWITCH_E2E_REPO=/tmp/rs-demo/demo go test -tags integration ./internal/lifecycle/ -run E2E -timeout 45m`. Expected: PASS.
- [ ] **Step 10:** Write the observed outputs into `docs/stage2-acceptance.md`. `make check && git add -A && git commit -m "test: Stage 2 end-to-end acceptance"`.
