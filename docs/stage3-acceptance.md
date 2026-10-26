# Stage 3 acceptance

This is the run procedure for the Stage 3 acceptance. Run it on a real host,
not in a sandbox. The reference host is CachyOS/Arch.

## Existing networks after upgrading

Networks railswitch created before Stage 3 keep their old options:
`isolate=true` (D50) only takes effect when podman creates the network again.
For every project created before Stage 3:

1. `railswitch down` in every worktree of the project;
2. `podman network rm <prefix>_proxy <prefix>-<slug>-dev …` (every worktree's
   dev network, by exact name; `railswitch wt ls` lists the slugs);
3. `railswitch up` in every worktree. The network units create the networks
   again, now with `isolate=true`.

Check with `podman network inspect <prefix>_proxy --format '{{.Options}}'`:
it shows `isolate:true`.

## Host source address (confirmation, optional)

Every project router admits only clients in the project's subnet or the host
(D50). The host clause, `machine.HostClientClause()`, is pinned without this
measurement: it admits host traffic arriving from outside 10.214.0.0/16 (a)
and from the receiving Traefik address `10.214.<n>.2` (b):

```
!ClientIP(`10.214.0.0/16`) || ClientIP(`10.214.0.2/32`) || … || ClientIP(`10.214.255.2/32`)
```

The check that it works is Task 9's host run (`TestE2ETwoProjects`: the app,
an `s3.` URL, `logs.localhost` and `traefik.localhost` answer from the host
and refuse the other project's container). The measurement below only tells
which of (a) and (b) the host uses; any other outcome (c) means the clause is
wrong and every browser request on the host gets a 404 from Traefik.

With a project up (prefix `<prefix>`, worktree `<slug>`):

```sh
podman info --format '{{.Host.NetworkBackend}} {{.Host.RootlessNetworkCmd}}'
podman run -d --rm --name rs-whoami --network <prefix>_proxy -p 127.0.0.1:18999:80 docker.io/traefik/whoami
podman inspect rs-whoami --format '{{(index .NetworkSettings.Networks "<prefix>_proxy").IPAddress}}'   # whoami's IP
curl -s http://127.0.0.1:18999/ | grep RemoteAddr                                  # host
podman exec <prefix>-<slug>-rails curl -s http://<whoami IP>/ | grep RemoteAddr    # the project's rails container
podman rm -f rs-whoami
```

Record here:

| What | Value |
|---|---|
| `podman info` line | |
| whoami's IP on `<prefix>_proxy` | |
| host curl's `RemoteAddr` | |
| rails container's `RemoteAddr` | |
| Outcome | (a) outside 10.214.0.0/16 / (b) whoami's own address / (c) other |
