# Virtual Office sets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The Virtual Office draws its art from a swappable set: `cyberpunk` built in, others installed per machine with `railswitch office set`, checked against a fixed list of pieces.

**Architecture:** A new Go package `internal/office` owns the piece list (`pieces.json`), glTF bounds, set checks and the install store under `<data>/office/`. The CLI (`internal/cli/office.go`) exposes `office set check|add|ls|use|rm`; the active set is `dashboard.toml` `[office] set` (`internal/dashconf`). The dashboard serves `set.json` and installed files; the page (`static/office/`) loads the set, draws a box for each missing piece, and reads heights and the look from the set.

**Tech Stack:** Go (stdlib + BurntSushi/toml), three.js (vendored), Blender 4.2+ Python for the kit.

**Spec:** `docs/superpowers/specs/2026-11-02-virtual-office-sets-design.md`

## Global Constraints

- Dependencies: stdlib, `github.com/BurntSushi/toml`, `github.com/coder/websocket` only.
- `make check` passes before every commit; unit tests need no podman, systemd, gh, network or browser.
- Every read command takes `--json` (D40), schema in `docs/json.md`; every `e.flags(name)` has a `help.go` synopsis.
- Set name: `^[a-z][a-z0-9-]{0,30}$`, not `issues`, `prs`, `notes`, `whiteboard`, not a built-in name.
- A piece matches when its box (`min`, `max`, colliders left out) is within ±0.02 m of the list's on every axis (amended spec: today's origins don't follow one anchor rule).
- Folder limits: ≤1,000 files, each ≤16 MiB, ≤256 MiB in all; no symlinks (the folder included), regular files only; glTF URIs relative, no scheme (`data:` included), never leaving the folder.
- `set.toml`: `schema = 1`, strict (unknown keys refused), colours `#rrggbb`, intensities 0..10, `fog_density` 0..1, `bloom` 0..3.
- Installed sets: `<data>/office/<name>/` (`machine.DefaultDirs().Data`), written only by `office set add` (temp dir under `<data>/office/`, renamed over); `index.json` `{files: {<path>: <sha256>}, pieces: [names], added}`.
- Remove by exact path; secrets: none involved.
- With `cyberpunk` the office must look as it does today.

## Review Focus

1. **A set folder given with a trailing slash, `.` or a relative path** → `check`/`add` work the same as with the absolute path (Task 3 test `TestCheckDirForms`).
2. **Re-adding the active set while the dashboard serves it** → the rename is atomic; a request mid-add gets the old or the new file, never a half-written one (Task 4 test `TestAddReplaceAtomic`: old dir renamed aside, then removed).
3. **`dashboard.toml` written by the colours editor after `office set use`** → `[office] set` survives a colours save, and colours survive `use` (Task 4 test `TestDashconfOfficeRoundTrip`).
4. **An installed set whose folder was deleted by hand while active** → `ls` shows it gone, `set.json` falls back to `cyberpunk` with `fallback` (Task 5 test in `TestOfficeSetJSON`).
5. **A glTF whose root node is scaled/rotated (Blender's Y-up conversion)** → bounds are in the piece's frame after node transforms (Task 3 test `TestBoundsTransforms`).

---

### Task 1: Move the files to the Virtual Office layout (no behaviour change)

**Files:**
- Move: `internal/web/static/cyberpunk/*` → `internal/web/static/office/` (`cyberpunk.js` → `office.js`, `cyberpunk.css` → `office.css`)
- Move: `internal/web/static/office/kit.js` → `internal/web/static/office/kit/kit.js`
- Move: `internal/web/static/office/models/*` → `internal/web/static/office/sets/cyberpunk/`
- Move: `internal/web/cyberpunk.go` → `office.go`, `cyberpunk_test.go` → `office_test.go`, `templates/cyberpunk.tmpl` → `templates/office.tmpl` (template name `office`)
- Modify: `internal/web/state.go`, `server.go` (SSE `view=cyberpunk` → `view=office`), imports in `world.js` (`./kit/kit.js`), the models URL `'/static/office/sets/cyberpunk/'`, `tools/office-kit/build_kit.py` `OUT`, `tools/office-kit/preview.html`, `internal/naming/naming_test.go` if it names paths, `docs/office-kit.md`, `docs/dashboard.md`, `AGENTS.md` package map.

**Interfaces:**
- Produces: the layout every later task uses. Go handler names keep their `cyber*` identifiers only where renaming would churn; the template is `office`.

- [ ] **Step 1:** `git mv` the files above; fix every reference found by `grep -rn "static/cyberpunk\|office/models\|office/kit.js\|cyberpunk.tmpl\|view=cyberpunk\|\"cyberpunk\"" internal tools docs AGENTS.md` (the set name `cyberpunk` in the route `/virtual-office/cyberpunk` stays).
- [ ] **Step 2:** `make check` → passes (existing `TestCyber*`/office page tests and `noCSPViolation` cover the page).
- [ ] **Step 3:** Manual: open `/p/<prefix>/virtual-office/cyberpunk` on the manual server; the lobby and a floor load with no console errors and look as before.
- [ ] **Step 4: Commit** `Virtual Office: files under static/office/, the models as the cyberpunk set (D153)`

### Task 2: `pieces.json`, points and `set.toml` (done: Blender-free generator)

**Files:** `tools/office-kit/pieces.py` (new: reads the exported glTFs, writes `kit/pieces.json` with `min`, `max`, `points` from its `POINTS` table, `extras` [`collider`, `parts`, `accent`] and `parts`; bumps `version` when a shared piece's box (>0.001 m) or points change; a `self_check` of that rule runs every time), `build_all.py` (runs it last unless `OFFICE_KIT_OUT` is set), `tools/office-kit/stub_set.py` (new), `sets/cyberpunk/set.toml` (new, today's look).

- Points: `Furn_Desk.top` 0.75, `Furn_CoffeeCounter.top` 0.94, `Furn_CoffeeTable.top` 0.42, `Furn_Arcade_Stand.top` 0.78, `Furn_ParrotPerch.bar` 1.284, `Deco_Crate.top` 0.46 (all at x = z = 0).
- A rebuild reproduces the models (glTF JSON identical but for the scene name `KitBuild`/`Scene`; `.bin` float noise inside the same bounds), so the models are not re-committed.

### Task 3: `internal/office`: piece list, glTF bounds, set checks

**Files:**
- Create: `internal/office/pieces.go`, `gltf.go`, `set.go`, `check.go`, and tests `gltf_test.go`, `check_test.go`, `pieces_test.go`
- Modify: `internal/web/server.go`: export `func StaticFS() fs.FS` (the embedded `static` sub-FS) so the CLI reads built-ins and `pieces.json` without a second embed.

**Interfaces:**
- `type Piece struct { Min, Max [3]float64; Points map[string][3]float64; Extras, Parts []string }` (JSON `min`, `max`, `points`, `extras`, `parts`)
- `type Pieces struct { Version int; Pieces map[string]Piece }`; `func LoadPieces(fsys fs.FS) (*Pieces, error)` reads `office/kit/pieces.json`.
- `type Look struct { Background, Fog, Ambient, Key *string; FogDensity, AmbientIntensity, KeyIntensity, Bloom *float64; Scanlines *bool }` (JSON names snake_case as in `set.toml`)
- `type Set struct { Schema int; Name string; Version, Pieces int; Description string; Look Look }`; `func ParseSet(data []byte) (*Set, error)`.
- `func Bounds(gltf []byte) (min, max [3]float64, err error)`: every mesh primitive's POSITION accessor `min`/`max` corners transformed by the node's world matrix (TRS or `matrix`), from the default scene's root nodes, skipping nodes matching `_COL\d*$` and their subtrees (as `pieces.py` does).
- `type Result struct { Piece, Status, Reason string; Min, Max *[3]float64 }` (`Status` `ok|missing|refused`)
- `type Report struct { Name string; Version, Pieces int; Look Look; Results []Result; Errors []string }`; `func (r *Report) OK() bool` (no refused, no errors).
- `func Check(dir string, list *Pieces) (*Report, error)` reads the folder from disk as hostile (`os.Lstat` each entry; no `safefile.TreeCopy` needed). Error only for an unreadable dir; every problem is in the report.
- `var ReservedNames = []string{"issues", "prs", "notes", "whiteboard"}`; `func ValidName(s string) error`.

- [ ] **Step 1: Write failing tests**
  - `TestBoundsTransforms`: hand-written glTF JSON with a root node `rotation` (−90° about X), `scale` 2, a nested child `translation`, two meshes; expect exact min/max.
  - `TestCheck` table: ok piece; 3 cm too wide → `refused` with reason `Furn_Desk: x -1.03..1.00, y 0.00..0.75, z -0.40..0.40 m; the list says x -1.00..1.00, y 0.00..0.75, z -0.40..0.40 (±0.02)`; the origin moved 10 cm (same size) → refused; unknown `.gltf` name → refused; piece not present → `missing`; folder refusals each give an error and `!OK()`: symlinked file, the folder itself a symlink, `../x.bin` URI, `/abs.bin`, `data:` URI, `http://` URI, a 17 MiB file, a stray `notes.txt`, 1,001 files (sparse/empty files are fine).
  - `TestParseSet`: unknown key refused; `#abc` refused; `bloom = 4` refused; `schema = 2` refused as newer; name `notes` refused; valid file parses.
  - `TestCheckDirForms`: same report for `dir`, `dir/`, and a relative path.
  - `TestCyberpunkMatchesPieces` (`pieces_test.go`): `Check` on the real folder `../web/static/office/sets/cyberpunk` (tests run in the package dir) → every piece `ok`, no `missing`, every glTF has a list entry. Built-ins at runtime are copied nowhere: `Store` checks them from `web.StaticFS()` by writing nothing and reading through `fs.FS` only for `Ls`'s counts, never `Check`.
- [ ] **Step 2:** `go test ./internal/office/` → FAIL (undefined).
- [ ] **Step 3:** Implement. Reason numbers: `%.2f`. A glTF that doesn't parse, has no mesh, or an accessor without `min`/`max` → refused with that reason. Referenced files = `buffers[].uri` and `images[].uri`, URL-unescaped, `path.Clean`, must not start with `..` or `/`.
- [ ] **Step 4:** `go test ./internal/office/` → PASS; `make check` → passes.
- [ ] **Step 5: Commit** `office: the piece list, glTF bounds and set checks (D153)`

### Task 4: The store and `railswitch office set check|add|ls|use|rm`

**Files:**
- Create: `internal/office/store.go`, `store_test.go`; `internal/cli/office.go`, `office_test.go`
- Modify: `internal/dashconf/dashconf.go` (+ test): `File.Office struct{ Set string }`, read/written as `[office] set`; `internal/cli/cli.go` (`"office": cmdOffice`), `help.go` synopses, `completion.go` (+ `task_test` list), `docs/json.md`, `docs/completion.md`.

**Interfaces:**
- `type Entry struct { Name, Source string; Version, Pieces, Present, Missing int; Status, Description string; Look Look; Have []Have; Dir string }` (`Source` `builtin|installed`, `Status` `ok|stale|broken`; `Have{Piece string; Extras []string}` only for the active set; `Dir` installed only, not in JSON).
- `type Store struct { Data string; Builtin fs.FS; List *Pieces }`
- `func (s *Store) Add(dir string) (*Report, error)`; `func (s *Store) Ls(active string) ([]Entry, error)`; `func (s *Store) Remove(name string) error`; `func (s *Store) Get(name string) (*Entry, error)`.
- CLI JSON: `office set check` → `Report`; `office set ls` → `{active, sets[]}`.

- [ ] **Step 1: Write failing tests**
  - `TestAddReplaceAtomic`: add set `cabin` v1, then v2; after the second add only v2's files exist, no temp dirs remain under `<data>/office/`, `index.json` lists every file with its sha256.
  - `TestAddRefuses`: a refused check adds nothing; name `cyberpunk` refused.
  - `TestLsStatus`: built-in `cyberpunk` `ok`; installed set with `pieces = 0` → `stale`; after editing one installed file → `broken`; after `rm -r` by hand → not listed.
  - `TestDashconfOfficeRoundTrip`: `Set("light", …)` then `Save`, set `Office.Set = "cabin"`, `Save`, `Load` → both kept; a file without `[office]` loads with `Set == ""`.
  - CLI `TestOfficeSet`: `check` exit 0/1 and JSON fields; `use unknown` exit 1; `use` a stale set exit 1; `rm cyberpunk` exit 1; `rm` the active set clears `[office] set`; `ls --json` `active` defaults to `cyberpunk`.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement. `Add`: `Check`; copy files into `os.MkdirTemp(<data>/office, ".add-*")`, write `index.json`, `os.Rename` the old dir to `.old-*`, rename temp to `<name>`, remove `.old-*` by exact path. Dirs 0700, files 0600. `Ls` verifies hashes of installed sets.
- [ ] **Step 4:** Run → PASS; `make check`.
- [ ] **Step 5:** Docs: `docs/json.md` (`office set check`, `office set ls`), `docs/completion.md`. Commit `office set check|add|ls|use|rm (D153)`.

### Task 5: The dashboard: set.json, installed files, routes, the Machine picker

**Files:**
- Modify: `internal/web/office.go`, `server.go`, `signers.go` (machine page data), `templates/machine.tmpl`, `office_test.go`, `docs/dashboard.md`

**Interfaces:**
- `GET /office/set.json` → `{name, base, version, look, have, fallback?}` from `railswitch --json office set ls`; `base` `/static/office/sets/<name>/` or `/office/sets/<name>/`.
- `GET /office/sets/{name}/{path...}`: path in `index.json`. The dashboard has no data dir today: add `Data string` to `web.Config`, set from `machine.DefaultDirs().Data` where the dashboard starts; read `<Data>/office/<name>/index.json` and the file, regular, no symlink (`os.Lstat`), ≤16 MiB, `Content-Type` by extension (`.gltf` `model/gltf+json`, `.bin` `application/octet-stream`, `.png`, `.jpg`).
- `GET /p/{prefix}/virtual-office/{set}`: the page when `{set}` is built in or installed (else 404); the bare path redirects to the active set. Keep the fixed API routes.
- `POST /machine/office/use` (form `name`, `ValidName`) → job `office set use <name>`, back to `/machine`.

- [ ] **Step 1: Write failing tests**
  - `TestOfficeSetJSON`: active `cabin` installed → base `/office/sets/cabin/`; active set missing from `ls` → `cyberpunk` with `fallback`.
  - `TestOfficeSetFiles`: indexed file 200 with its bytes; unindexed 404; `..` 404; a symlink in place of an indexed file 404.
  - `TestOfficeRoutes`: `/virtual-office/cyberpunk` 200, `/virtual-office/cabin` 200 when installed, `/virtual-office/nope` 404, `/virtual-office` → 302 to the active set; `/virtual-office/notes` still the notes API.
  - `TestReservedSetNamesMatchRoutes`: the fixed paths registered under `/p/{prefix}/virtual-office/` (walk `server.go`'s route list, or a shared slice used to register them) equal `office.ReservedNames`.
  - `TestMachineOfficePicker`: machine page lists the sets and posts to `/machine/office/use`; a bad name is 400.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. **Step 4:** Run → PASS; `make check`.
- [ ] **Step 5:** `docs/dashboard.md`: the Virtual Office section, the set picker, the routes. Commit `Dashboard: the active office set, installed sets' files, the Machine picker (D153)`.

### Task 6: The page reads the set

**Files:**
- Modify: `static/office/kit/kit.js`, `world.js`, `film.js`, `rooms.js`, `office.js`, `office.css` (banner, if new)

**Interfaces:**
- `loadOfficeKit(set, pieces, names, loader)`: `set` is `set.json`; `pieces` is `pieces.json`; loads `set.base + name + '.gltf'` for each name in `set.have`, else a box.
- `kit.spawn(name)`: model or box from `min` to `max` (`RS_Placeholder`, light grey `#9aa0a8`, edges `#5a6068` via `EdgesGeometry`), `userData.missing = set.name`; unknown name → red box + `console.error`.
- `kit.point(piece, name) → [x, y, z]`; `kit.colliders` adds one box from `min` to `max` for a box piece whose extras include `collider`.
- A model that fails to load → its box + `console.error`.
- `set.look` drives `film.js` (background, fog colour/density, hemi colour, hemi levels × `ambient_intensity`, city light colour/intensity) and `world.js` (bloom strength; CRT effect on/off).

- [ ] **Step 1:** `office.js` fetches `/office/set.json` and `/static/office/kit/pieces.json` before the first room; shows `fallback` as `.hud-banner`.
- [ ] **Step 2:** Replace every point literal from Task 2 in `rooms.js` with `k.point(...)`; tooltips of missing pieces say `missing in <set>`.
- [ ] **Step 3:** `make check` (CSP test, no inline style). Manual: cyberpunk looks as before (screenshots before/after, lobby and a floor); install a test set with three pieces copied from cyberpunk and another `[look]` → the rest are boxes, colours change; delete one of its glTFs by hand → `broken`, banner shown.
- [ ] **Step 4: Commit** `Virtual Office: the page draws the active set, boxes for missing pieces (D153)`

### Task 7: Docs and the roadmap row

**Files:** `docs/office-kit.md` (sets, `pieces.json`, `set.toml`, building a set with `stub_set.py` and `office set check`), `docs/guide/` if it mentions the office, `AGENTS.md` (package map: `internal/office`, the `static/office/` layout; the Security rule's exception for installed sets' files), roadmap `| D153 |` row.

- [ ] **Step 1:** Write them; the D153 row gives the reason for each decision in the spec, including the exception to "reads only through `railswitch --json`".
- [ ] **Step 2:** `make check`; commit `docs: Virtual Office sets (D153)`; push and open the PR.
