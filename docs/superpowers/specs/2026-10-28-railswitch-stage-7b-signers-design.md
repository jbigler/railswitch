# railswitch Stage 7b: signers

Design for Stage 7b (D95). Agreed with the user on 2026-10-28, sections 1
and 2; section 3 (errors, testing) written on the user's instruction to
continue without further questions. Decisions D99–D102.

## Intent

HANDOFF §3.11: "an unsigned library, or a signer not yet trusted, is shown
but can't be installed". 7b decides who is trusted to publish packages, how
a signer moves to a new key and how a key is withdrawn. 7c (library
repositories) fetches, browses and installs; it calls 7b's verifier. 7b
ships the verifier, the trust store, the publisher's tools and the Signers
section of the machine page, and is testable on its own against a local
folder.

## The signed index (D99)

A library is a folder laid out like the built-ins (`packs/<name>/`,
`options/<name>/`, `agents/<name>/`) with `library.toml` and
`library.toml.sig` at its top.

```toml
schema = 1
library = "github.com/acme/stack-library"   # the library's id, inside the signed bytes
serial = 7                                   # never goes down (rollback)

[[package]]
ref = "django@1"
kind = "pack"                                # pack | option | agent
sha256 = "<pack.ContentHash>"

[[rotate]]                                   # optional: an old key vouches for a new one
from = "SHA256:<old fingerprint>"
to = "ssh-ed25519 AAAA…"
signature = """-----BEGIN SSH SIGNATURE-----…"""

[[revoke]]                                   # optional: keys never trusted again here
fingerprint = "SHA256:<fingerprint>"
```

- `library`: `[a-z0-9][a-z0-9./_-]{0,199}`, no `..`. `serial`: 1 … 2^53.
  Refs as `pack.ParseRef`, built-in refs only (no `local:`); each ref
  once.
- `library.toml.sig`: `ssh-keygen -Y sign -n railswitch-library` over the
  exact bytes of `library.toml`.
- A rotation's `signature` is by the old key, namespace
  `railswitch-rotate`, over the message `railswitch-rotate\n<library>\n<to>\n`.
- Package content is `pack.ContentHash` of the package directory, the
  hash `stack.lock` pins, so 7a's review and store apply to library
  packages unchanged.

## Trust (D100)

`~/.config/railswitch/signers.toml` (`schema = 1`, 0600, atomic writes under a flock on `signers.toml.lock`):

```toml
schema = 1
[[library]]
id = "github.com/acme/stack-library"
last_serial = 7
last_index = "<sha256 of serial 7's library.toml>"
[[library.key]]
key = "ssh-ed25519 AAAA… "
fingerprint = "SHA256:…"
added = 2026-10-28
[[library.revoked]]
fingerprint = "SHA256:…"
```

- Trust is per library: a key trusted for one library never verifies
  another.
- Fingerprints are computed in Go (sha256 of the key blob, unpadded
  base64, `SHA256:` prefix), never parsed from a tool's output.
- Keys: `ssh-ed25519`, `ecdsa-sha2-nistp256|384|521`, `ssh-rsa`
  (≥ 3072 bits not checked; the type list only), `sk-ssh-ed25519@openssh.com`.
- Key blobs are parsed field by field and must be in the one encoding
  OpenSSH writes, since the fingerprint hashes the blob as given: nothing
  after the last field; ed25519 (and sk-ed25519) keys exactly 32 bytes, an
  sk key's application with no NUL; an ecdsa key's curve name its type's,
  its point uncompressed and on the curve; an RSA key's `e` and `n`
  positive mpints with no superfluous leading zero. Otherwise OpenSSH reads
  two blobs as one key and a revoked key returns under a new fingerprint.
  This holds for keys on a command line, in a signature, in `[[rotate]]`
  and in `signers.toml`, where each key must also be exactly
  `<type> <base64>` (no comment, no control characters).

## Verifying

`internal/signing`: `Verify(ctx, run sys.Runner, dir string, t *Trust) (*Result, error)`.

1. Read `library.toml` and `.sig` through `safefile.Read`; decode strictly
   (`tomlfile.Decode`, schema 1).
2. The library's trust entry by `library`; none → `unknown-signer` (the
   signing key's fingerprint from the signature, see below).
3. `ssh-keygen -Y verify -f <allowed> -I <library> -n railswitch-library
   -s <sig>` with the file on stdin; `<allowed>` a temp file (0600, under
   `$XDG_RUNTIME_DIR`, removed) listing only that library's trusted, not
   revoked keys as `<library> namespaces="railswitch-library" <key>`.
   Through `sys.Runner`, argv, 30 s.
4. Not verified by a trusted key: if a `[[rotate]]` entry's `from` is a
   trusted key, its signature verifies (same tool, namespace
   `railswitch-rotate`, the old key only), and the index verifies with
   `to` → `rotation` (old and new fingerprints); else `unknown-signer`
   with the signing key's fingerprint (`ssh-keygen -Y check-novalidate`
   gives none; the fingerprint is read from the signature blob in Go: the
   SSHSIG armour's public key field).
5. Signed by a revoked fingerprint (the library's revoked list) →
   `revoked`. `serial` < `last_serial`, or `serial` = `last_serial` with
   a `library.toml` whose sha256 is not `last_index` → `rolled-back`
   ("serial N was already used by another index").
6. Each `[[package]]`: the directory `<kind>s/<name>/` loads (`pack.Load`
   over just that dir) and its `ContentHash` equals `sha256` → `signed`;
   else not. A package directory the index doesn't list is `signed: false`.
   Under any status but `trusted`, every package is `signed: false` with
   the reason `the index is not trusted`.

`Result{Status, Reason, Library, Serial, Fingerprint, Key, NewFingerprint, NewKey, KnownLibrary, Packages []PackageResult{Ref, Kind, Dir, Signed, Reason}}` (`Key`/`NewKey` the keys `library trust` adds; `Reason` text from the library is printed through `safefile.Printable`). Status `trusted` records
`last_serial` and `last_index` and applies `[[revoke]]` (moves those fingerprints from keys
to revoked).

## CLI (D101)

- `signer ls [--json]`: per library, keys (fingerprint, key, added),
  revoked, last_serial.
- `signer add <library> <public-key-file|->`: trusts a key for a library;
  prints its fingerprint; refuses a revoked one and a malformed key.
- `signer rm <library> <fingerprint>`: distrusts it; `signer add` or
  `library trust` can trust it again. An entry with no keys left is
  removed only when it also has no revocations and no `last_serial`;
  otherwise it stays, so `rm` then `add` can't undo a revocation or
  accept an older index.
- `signer revoke <library> <fingerprint>`: revokes it: moves it from the
  keys to the revoked list (a fingerprint not trusted yet can be revoked
  ahead). A revoked key is never trusted again for that library: `signer
  add`, `library trust` (with or without `--new-signer`) and rotations
  refuse it, and an index it signs is `revoked`. No command undoes it.
- `library verify <dir> [--json]`: the Result; exit 0 when `trusted`, 1
  otherwise (the JSON is printed either way).
- `library trust <dir> --fingerprint <fp> [--rotation | --new-signer]`:
  re-verifies; the reported fingerprint (or, with `--rotation`, the new
  one) must equal `<fp>`; adds the key (a rotation keeps the old key; a
  later index's `[[revoke]]` retires it). An `unknown-signer` for a library
  whose entry already has keys or revocations (`known_library: true` in
  the Result; `verify` warns that the library already trusts other keys
  and none vouched for this one) is refused without `--new-signer`. A
  first trust (no entry) needs no flag.
- Publisher: `library index <dir> --library <id> [--serial <n>]` writes
  `library.toml` from the packages in `<dir>` (serial: given and above
  the existing one, else the existing one + 1, else 1; keeps
  `[[rotate]]`/`[[revoke]]` already there);
  `library sign <dir> --key <private-key-file>` runs `ssh-keygen -Y sign`
  (no timeout: it may prompt for a passphrase on the terminal);
  `library rotate <dir> --old-key <file> --new <pubkey-file>` adds a signed
  `[[rotate]]` and bumps the serial (a serial names one index).

## Revocation's effect (D102)

Revoking (locally with `signer revoke` or by an index's `[[revoke]]`) stops
installs and updates from that library by that key. Projects already using
its packages keep running (pinned by hash, reviewed at install).
`overview --json` gains `signer_warnings: []` (always empty in 7b; 7c fills
it when library packages can be installed).

## Page

The machine page gains a Signers section from `signer ls --json`: each
library, its keys (fingerprint, key type, added), its revoked fingerprints;
Distrust (confirm; job `signer rm <library> <fp>`); Revoke (confirm, saying
it can't be undone from the CLI; job `signer revoke <library> <fp>`); Add key (library id, the public
key pasted: job `signer add <library> -` with the key on stdin). "Trust
this key" and "Confirm rotation" come with 7c's library pages.

## Errors

- A malformed `library.toml` or signature: `bad-signature` with the
  reason; never `trusted`.
- `ssh-keygen` missing: an error naming it (doctor gains a check:
  `ssh-keygen -Y` support).
- Concurrent writes of `signers.toml`: every load, verify and save
  (`library verify`, `library trust`, `signer add`/`rm`/`revoke`) runs
  under an exclusive flock on the sibling `signers.toml.lock` (0600),
  through `signing.UpdateTrust`, the only writer; so no writer drops
  another's revocation or lowers `last_serial`. The file is replaced
  atomically, so `signer ls` reads without the lock.

## Known limits

- A fingerprint names a key encoding, not a private key: the holder of a
  revoked Ed25519 key can present the same point as an `sk-ssh-ed25519`
  key (any application string), and an RSA key holder can pick another
  exponent, each with a new fingerprint. That gets no further than a new
  key would: the signer is unknown, and on a known library (keys,
  revocations or a verified serial) trusting it needs `--new-signer`
  behind a warning.

## Testing

- `signing` with real `ssh-keygen` (allowed in unit tests like git; keys
  generated with `ssh-keygen -t ed25519 -N ''` in `t.TempDir()`): trusted;
  unknown signer reports the right fingerprint; a key trusted for another
  library fails; a tampered `library.toml` byte fails; a package byte
  changed → `signed: false`; rollback; revoked; a vouched rotation →
  `rotation`, then trusted after `library trust --rotation`; a rotation
  signed by an untrusted key → `unknown-signer`; `[[revoke]]` applied on
  trusted.
- Fingerprints equal `ssh-keygen -lf`'s.
- CLI: `signer add/rm/ls`; `library trust` with a wrong fingerprint
  refuses; `library index` + `sign` + `verify` round trip.
- Web: the Signers section; Add key's job has the key on stdin and the
  argv exact; Distrust argv; guard.

Host acceptance, `docs/stage7b-acceptance.md`: a local library folder
with a pack copied from a built-in; index, sign, verify (unknown signer),
trust, verify (trusted); change a byte (not signed); rotate; revoke; an
older serial refused; the machine page lists and distrusts.

## Decisions (roadmap)

- D99: a library is signed as one index, `library.toml` (refs and content
  hashes, serial, rotations, revocations), with an SSH signature
  (`ssh-keygen -Y`, namespace `railswitch-library`).
- D100: signer trust is per library, added on purpose (a fingerprint
  checked with the publisher, or `signer add`), in `signers.toml`.
- D101: rotation is vouched by the old key and confirmed once; revocations
  in a trusted index apply at once and can't be undone from the CLI; the
  serial never goes down.
- D102: revocation stops installs and updates; running projects keep
  their pinned packages, with a warning (7c).
