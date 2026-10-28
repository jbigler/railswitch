# Stage 7b acceptance: signers

Host run on 2026-10-29 (OpenSSH 10.6p1, Omarchy) with the branch build
installed. The CLI part used a scratch `XDG_CONFIG_HOME`, so the real
`signers.toml` got no test entries; the page part used the real one and
left it empty. The library was a local folder with one pack, `demo@1`
(a copy of the built-in rails pack, renamed), and two fresh ed25519 keys.

## CLI

| Check | Result |
|---|---|
| `library index` + `library sign` (key 1); `library verify`: `unknown-signer`, `demo@1` signed | PASS |
| `library trust --fingerprint <key 1>`; verify: `trusted`, serial 1 recorded | PASS |
| `library trust` with another fingerprint: refused, nothing written | PASS |
| One byte added to a pack file: `demo@1` `signed: false`; put back: `signed: true` | PASS |
| Serial 2 signed and verified; the serial 1 copy then: `rolled-back` ("serial 1 is below 2") | PASS |
| `library rotate` (key 1 vouches for key 2), signed by key 2: `rotation`; `library trust --rotation`: `trusted` | PASS |
| `[[revoke]]` key 1 in an index signed by key 2: `trusted`, `signer ls` lists key 1 as revoked | PASS |
| A later index signed by key 1: `revoked`; `signer add` of key 1: refused ("is revoked") | PASS |

## Machine page

| Check | Result |
|---|---|
| `/machine` lists "No libraries have trusted keys" | PASS |
| Add key (library id, pasted public key): the job runs, the library shows with the key's fingerprint and date | PASS |
| Distrust (confirm): the key and the empty library entry are gone; `signers.toml` 0600 | PASS |

## Known limits

- Nothing installs from a library yet (7c): verify reports what an install
  would accept. `overview`'s `signer_warnings` stays empty.
- "Trust this key" and "Confirm rotation" have no page until 7c's library
  pages; they are CLI only (`library trust`).
