# Libraries: publishing and trusting signed packages

A **library** is a folder of packages that someone other than railswitch
publishes. railswitch installs a library's packages only when the
library's signed index verifies against a key you trust for that library
(D99, D100). Stage 7b ships the index, the signing tools and the trust
store. Stage 7c fetches libraries and installs from them.

This guide has two parts. Publishers build, sign and rotate an index.
Users verify a library and decide which keys to trust.

## Requirements

- OpenSSH 8.2 or newer: railswitch signs and verifies with
  `ssh-keygen -Y`. `railswitch doctor` checks it (`ssh-keygen -Y`).
- An SSH key for signing. Use a key that signs only this library, not your
  login key. Types: `ssh-ed25519` (recommended),
  `ecdsa-sha2-nistp256|384|521`, `ssh-rsa`, `sk-ssh-ed25519@openssh.com`.
  railswitch refuses a key blob that is not in the encoding `ssh-keygen`
  writes, such as an RSA number with a leading zero byte or bytes after
  the last field. Such a blob is the same key to OpenSSH but would get
  another fingerprint, so a revoked key could come back under a new name.

## Library layout

A library is laid out like railswitch's built-in packages, with the index
and its signature at the top:

```
stack-library/
  library.toml          the signed index
  library.toml.sig      its signature
  packs/django/         pack.toml and its files
  options/clickhouse/   option.toml and its files
  agents/aider/         agent.toml and its files
```

Each package directory must load as a package (docs/packs.md). The index
lists each package's ref, kind and content hash. The content hash is
`pack.ContentHash`, the hash that `stack.lock` pins, so a library package
gets the same update review (`lock --review`) as a built-in one.

## The index

```toml
schema = 1
library = "example.org/acme/stack-library"
serial = 7

[[package]]
  ref = "django@1"
  kind = "pack"
  sha256 = "ed2b7198a78ee84c2ed316f544ff7bdde8d4ae3e7996e781c685899b30badb4b"

[[rotate]]
  from = "SHA256:<old fingerprint>"
  to = "ssh-ed25519 AAAA…"
  signature = "-----BEGIN SSH SIGNATURE-----\n…"

[[revoke]]
  fingerprint = "SHA256:<fingerprint>"
```

| Field | Meaning |
|---|---|
| `library` | The library's id: `[a-z0-9][a-z0-9./_-]{0,199}`, no `..`. It is inside the signed bytes, so an index can't be passed off as another library's. |
| `serial` | 1 to 2^53. Each published index gets a new serial, higher than the last. A user who has verified serial 7 refuses serial 6, and a serial-7 index other than the one they verified (`rolled-back`). |
| `[[package]]` | One per package: `ref` (`<name>@<N>`, no `local:`, each ref once), `kind` (`pack`, `option` or `agent`), `sha256`. |
| `[[rotate]]` | An old key vouches for a new one. See [Rotating the signing key](#rotating-the-signing-key). |
| `[[revoke]]` | A key never trusted again for this library. See [Revoking a key](#revoking-a-key). |

You don't write `library.toml` by hand. `library index` writes it.

## Publishing

1. Create a signing key:

   ```sh
   ssh-keygen -t ed25519 -f ~/.ssh/acme-library -C "acme stack-library"
   ```

2. Write the index from the packages in the folder:

   ```sh
   railswitch library index ./stack-library --library example.org/acme/stack-library
   ```

   The serial is the existing index's serial plus 1, or 1 for a new
   index. Pass `--serial <n>` to choose it. `library index` refuses a
   serial that is not above the current one, and an id other than the
   current index's. It keeps the current `[[rotate]]` and `[[revoke]]` entries. A
   package that doesn't load is an error, and nothing is written.

3. Sign the index:

   ```sh
   railswitch library sign ./stack-library --key ~/.ssh/acme-library
   ```

   This runs `ssh-keygen -Y sign` in the namespace `railswitch-library`
   and writes `library.toml.sig`. It has no timeout and uses your
   terminal, so ssh-keygen can ask for the key's passphrase or a
   security key's touch. To sign with a key held in `ssh-agent`, pass the
   `.pub` file as `--key`.

4. Give your users the library id and the key's fingerprint through a
   channel other than the library itself, such as your website or a
   signed release note:

   ```sh
   ssh-keygen -lf ~/.ssh/acme-library.pub
   ```

5. Publish the folder.

Repeat steps 2, 3 and 5 for each release. Any change to `library.toml`
needs a new signature.

## Rotating the signing key

To move to a new key, the old key vouches for it in the index. Users who
trust the old key confirm the new one once.

1. Create the new key, as in Publishing step 1.

2. Add the rotation, signed by the old key:

   ```sh
   railswitch library rotate ./stack-library --old-key ~/.ssh/acme-library --new ~/.ssh/acme-library-2.pub
   ```

   This signs the message `railswitch-rotate\n<library>\n<new key>\n` with
   the old key in the namespace `railswitch-rotate`, and adds a
   `[[rotate]]` entry to `library.toml` at the next serial. Running it
   again for the same two keys replaces the entry, again at a new serial.

3. Sign with the new key. If packages changed, run `library index` first.

   ```sh
   railswitch library sign ./stack-library --key ~/.ssh/acme-library-2
   ```

A user's `library verify` then reports `rotation`. After they confirm it
with `library trust --rotation`, they trust both keys. To retire the old
key, revoke it in a later index.

## Revoking a key

Add the key's fingerprint to `library.toml`, then index and sign with a
key that stays trusted:

```toml
[[revoke]]
  fingerprint = "SHA256:<old fingerprint>"
```

When a user verifies an index with this entry and the result is
`trusted`, railswitch moves that fingerprint from their trusted keys to
their revoked list for this library. From then on, an index signed by that
key is `revoked`, and `signer add` refuses the key. A user can't undo a
revocation from the CLI. A user can also revoke a key themselves with
`signer revoke` (see [Managing trust](#managing-trust)).

Revocation stops installs and updates from that library by that key.
Projects that already use its packages keep running: `stack.lock` pins
them by hash, and they were reviewed when installed (D102).

## Trusting a library

Trust is per library. A key you trust for one library never verifies
another library's index.

1. Verify the folder:

   ```sh
   railswitch library verify ./stack-library
   ```

   A library you don't trust yet reports `unknown-signer` and the signing
   key's fingerprint.

2. Check that fingerprint with the publisher, then trust it:

   ```sh
   railswitch library trust ./stack-library --fingerprint SHA256:Ays0teoPOjVM/BphiuEfsQ7gCq7VhFUQd9F9RRAZJXU
   ```

   `library trust` verifies the folder again. It adds the key only when
   the fingerprint you pass is the one the folder reports. Otherwise it
   refuses and changes nothing. It then records the index's serial.

   If the publisher gives you the public key instead, add it directly:

   ```sh
   railswitch signer add example.org/acme/stack-library ~/Downloads/acme-library.pub
   ```

   `signer add <library> -` reads the key line from stdin.

3. Verify again. A trusted library reports `trusted` and lists each
   package as `signed` or `not signed`. A package is `not signed` when its
   content differs from the index, when it doesn't load, or when the index
   doesn't list it. Under any status other than `trusted`, every package
   is `not signed` (`the index is not trusted`).

### Confirming a rotation

When the publisher rotates keys, `library verify` reports `rotation` with
the trusted key and the new key's fingerprint. Check the new fingerprint
with the publisher, then confirm it:

```sh
railswitch library trust ./stack-library --fingerprint SHA256:FMVaaJrORVs5Xx60M485oK5AtUTpvWqQapBKMLidPeA --rotation
```

`--rotation` trusts only the new key that a trusted key vouches for.
Without `--rotation`, `library trust` refuses a rotation, and with it,
`library trust` refuses a signer that no trusted key vouches for.

### A new signer for a library you already trust

If you already trust a key for a library, have revoked one, or have
verified an index of it before (its last key since distrusted), an index
signed by a key that no trusted key vouches for is suspect. A publisher
who still holds the old key rotates instead. `library verify` warns:

```
WARNING: this library already trusts SHA256:Ays0…; this index is signed by a different key and no trusted key vouched for it.
```

and `--json` reports `"known_library": true`. `library trust` refuses
the key unless you pass `--new-signer`. Pass it only when the publisher
confirms the new key through another channel, for example after losing
the old key. A first trust, for a library with no entry in
`signers.toml`, needs no `--new-signer`. Trusting the signer of an index
that then verifies as rolled back trusts nothing.

## Adding a library repository

A library you install from is a git repository, added to the machine
under an alias (D103). The alias prefixes the library's refs:
`acme:django@1` is the package `django@1` of the library added as `acme`.

1. Add the repository:

   ```sh
   railswitch library add https://github.com/acme/stack-library --alias acme
   ```

   The alias matches `[a-z][a-z0-9-]{0,19}` and is not `local`. Pass
   `--branch <branch>` to fetch a branch other than the remote's default.
   The URL is an https, ssh, git or file URL, or a local path. `library add`
   records the library in `~/.config/railswitch/libraries.toml` and fetches
   it. If the fetch fails before it reaches a verdict (a wrong URL, a
   refused file), the library is not added.

2. The first fetch of a library reports `unknown-signer` and the signing
   key's fingerprint, unless you already trust the key. Check the
   fingerprint with the publisher, then trust it by the alias:

   ```sh
   railswitch library trust acme --fingerprint SHA256:Ays0teoPOjVM/BphiuEfsQ7gCq7VhFUQd9F9RRAZJXU
   ```

   This verifies the waiting fetch again, trusts the key and makes the
   fetch the library's current copy, as `library trust <dir>` does for a
   folder (`--rotation` and `--new-signer` too). To trust a folder whose
   name is an alias, write it as `./<name>`.

3. Fetch updates later:

   ```sh
   railswitch library fetch acme     # or: library fetch (every library)
   railswitch library ls             # --json: docs/json.md
   ```

`library rm acme` removes the library and its copy. It refuses while a
registered project's `project.toml` names a ref of it (`acme:…`), and names
those projects.

### What a fetch does

A library repository is hostile input (D104). A fetch:

- clones only the branch's last commit, with no file contents and no
  working tree, into a temporary directory under `$XDG_RUNTIME_DIR` that
  is always removed. No hook, LFS filter or prompt runs, and git may use
  only the https, ssh, git and file transports. The whole fetch has a
  5 minute deadline.
- reads the tree of `library.toml`, `library.toml.sig`, `packs/`,
  `options/` and `agents/`, and refuses the whole fetch when any of them
  holds a symlink or a submodule, or a path that is not a package file
  (`<kind>s/<name>/<file>`, where `<file>` is one the package format
  allows: docs/packs.md), or more than 2,000 files, a file over 1 MiB, or
  over 64 MiB in all. Other files in the repository are never fetched.
- writes those files (0644, `.sh` 0755) into a staging directory and
  verifies it against your trust, then acts on the verdict:

| Verdict | Effect |
|---|---|
| `trusted` | The fetch becomes the library's current copy. The library's id is recorded the first time; later fetches must have the same id. |
| `unknown-signer`, `rotation` | The fetch waits for `library trust <alias>`. The current copy, if any, stays. |
| `rolled-back`, `revoked`, `bad-signature` | The fetch is dropped. The current copy stays. |

A refused fetch, or one that fails (network, deadline), changes nothing.
`library add` and `library fetch` exit 0 for `trusted`, `unknown-signer`
and `rotation`, and 1 for any other verdict or a failure.

Each library has a directory in `~/.local/share/railswitch/libraries/<alias>/`:
`current/` (the verified copy that projects install from), `pending/` (a
fetch that waits for trust) and `fetch.toml` (current's id, commit,
serial and signer, and the last fetch's status, time and reason). The
current copy is out of every container's reach.

## Managing trust

```sh
railswitch signer ls
railswitch signer rm example.org/acme/stack-library SHA256:Ays0teoPOjVM/BphiuEfsQ7gCq7VhFUQd9F9RRAZJXU
railswitch signer revoke example.org/acme/stack-library SHA256:Ays0teoPOjVM/BphiuEfsQ7gCq7VhFUQd9F9RRAZJXU
```

- `signer rm` distrusts a key. Its signatures stop verifying, and you can
  trust it again later with `signer add` or `library trust`.
- `signer revoke` revokes a key. It moves the fingerprint to the
  library's revoked list, and the key is never trusted again for that
  library: `signer add`, `library trust` and rotations refuse it, and an
  index it signs is `revoked`. You can't undo it from the CLI. Use it
  when a key is lost or compromised. You can revoke a fingerprint that
  you don't trust yet.

Trust lives in `~/.config/railswitch/signers.toml` (mode 0600, under
`XDG_CONFIG_HOME` when it is set). Each command that changes it holds an
exclusive lock on `signers.toml.lock` from reading the file to writing
it, so two commands run at once never lose each other's changes. For each library, it holds the trusted
keys, the revoked fingerprints, `last_serial`, the highest serial
verified as trusted, and `last_index`, the sha256 of that index's
`library.toml`. When `signer rm` removes a library's last key, the
library's entry stays if it has revocations or a `last_serial`. So you
can't use `signer rm` and then `signer add` to accept an older index.

The dashboard's machine page lists the same trust, and can add, distrust
or revoke a key (docs/dashboard.md).

## Verdicts

| Status | Meaning | Next step |
|---|---|---|
| `trusted` | Signed by a key you trust for this library. The serial is above `last_serial`, or equal to it for the same index. | None. |
| `unknown-signer` | Validly signed by a key you don't trust for this library. | Check the fingerprint, then `library trust`. |
| `rotation` | Signed by a new key that a trusted key vouches for. | Check the new fingerprint, then `library trust --rotation`. |
| `rolled-back` | Older than an index you have already verified, or another index at the serial you verified. | Get the current index from the publisher. |
| `revoked` | Signed by a key revoked for this library. | Ask the publisher. |
| `bad-signature` | The index or the signature is malformed, or the signature does not cover the index. | Get an intact copy. |

`railswitch --json library verify <dir>` prints the verdict as JSON
(docs/json.md). Only `trusted` exits 0.
