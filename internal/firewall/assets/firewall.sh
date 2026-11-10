#!/usr/bin/env bash
# Inspect and adjust the per-container egress firewall that hook.sh (next to
# this script) loads into each container's network namespace. Host side only:
# nothing inside a container can reach these rules. Run by `railswitch fw`.
#
#   show [SVC]      every running firewalled container of this worktree (all
#                   worktrees from the workspace root), or only service SVC's:
#                   profiles, mode, and what was rejected -- or, in log mode,
#                   would have been. Also names any of the worktree's
#                   containers that run WITHOUT the firewall, which means the
#                   hook never fired.
#   allow [--for Ns] SVC HOST…
#                   add hostnames/IPs to a running container's allowlist, e.g.
#                   `railswitch fw allow nvim example.com`. Live only: gone on
#                   the next start. To keep it, add it to the profile's
#                   [firewall] allow.<profile> list in project.toml (an
#                   agent-<name>'s: [agents.<name>] allow). With --for <N>s
#                   the kernel drops it after N seconds (the allowt4 set,
#                   D179); railswitch checks the duration. A timed allow
#                   adds addresses only, not names to the DNS filter (D182).
#
# Environment (set by railswitch):
#   FIREWALL_DIR     <workspace>/.railswitch/firewall: the hook's state/ dir
#   PREFIX           the project prefix
#   WORKTREE         the current worktree's slug; empty at the workspace root
#   SELF_FIREWALLED  comma list of the service ids that run their own
#                    in-container firewall (firewall = "self": no hook)
set -euo pipefail
umask 077 # state files: only this user (as hook.sh)
PATH="$PATH:/usr/local/sbin:/usr/sbin:/sbin" # where the hook finds dnsmasq too (render.HookPathDirs)
TSV="${FW_FORMAT:-}" # tsv: show prints C/R lines for railswitch fw show --json
[[ -z "$TSV" || "$TSV" == tsv ]] || { printf 'FW_FORMAT must be tsv\n' >&2; exit 2; }

: "${FIREWALL_DIR:?FIREWALL_DIR unset (run through railswitch fw)}"
: "${PREFIX:?PREFIX unset (run through railswitch fw)}"
P="$PREFIX"
W="${WORKTREE:-}"
STATE="$FIREWALL_DIR/state"
TABLE=wt_firewall

die() { printf 'error: %s\n' "$*" >&2; exit 1; }

# podman unshare puts us in the rootless user namespace, where the container's
# network namespace can be entered with the needed privilege -- the same place
# the hook itself runs.
in_netns() { podman unshare nsenter -t "$1" -n "${@:2}"; }

# "addr proto port" per line from an nft set listing.
set_entries() {
  in_netns "$1" nft -n list set inet "$TABLE" "$2" 2>/dev/null \
    | tr -d '\n' \
    | sed -n 's/.*elements = {\([^}]*\)}.*/\1/p' \
    | tr ',' '\n' \
    | awk 'NF >= 5 { p = $3 == "6" ? "tcp" : $3 == "17" ? "udp" : $3; print $1, p, $5 }'
}

# self_firewalled reports whether container name runs a service listed in
# SELF_FIREWALLED.
self_firewalled() {
  local s list
  IFS=, read -ra list <<<"${SELF_FIREWALLED:-}"
  for s in ${list[@]+"${list[@]}"}; do
    [[ "$1" == "$P-"*"-$s" ]] && return 0
  done
  return 1
}

DNSDIR="$STATE/dns"

# stop_dns ends container id CID's DNS filter and drops its files.
stop_dns() {
  local p
  p="$(cat "$DNSDIR/$1.pid" 2>/dev/null)" || true
  # SIGKILL: the filter may have SIGTERM blocked (see hook.sh's stop).
  [[ "$p" =~ ^[0-9]+$ ]] && kill -KILL "$p" 2>/dev/null || true
  rm -f "$DNSDIR/$1.conf" "$DNSDIR/$1.pid" "$DNSDIR/$1.log"
}

# dns_refused CID: the names container CID's DNS filter refused (its
# "config <name> is NXDOMAIN" lines, log-queries), "<name> <lookups>" per
# line, the latest first, at most 50. The names are the container's to
# choose: only host names pass. The log grows with every query, so past
# 2 MiB it is cut to its last 1 MiB, which dnsmasq reopens on SIGUSR2.
dns_refused() {
  local log="$DNSDIR/$1.log" p
  [[ -f "$log" && ! -L "$log" ]] || return 0
  if (( $(stat -c %s "$log") > 2097152 )); then
    tail -c 1048576 "$log" >"$log.new" && mv -f "$log.new" "$log"
    p="$(cat "$DNSDIR/$1.pid" 2>/dev/null)" || true
    [[ "$p" =~ ^[0-9]+$ ]] && kill -USR2 "$p" 2>/dev/null || true
  fi
  tail -c 1048576 "$log" \
    | LC_ALL=C sed -n -E 's/^[A-Z][a-z]{2} +[0-9]{1,2} [0-9:]{8} dnsmasq\[[0-9]+\]: config ([A-Za-z0-9.-]{1,253}) is NXDOMAIN$/\1/p' \
    | LC_ALL=C tr 'A-Z' 'a-z' \
    | awk '/^[a-z0-9-]+(\.[a-z0-9-]+)+$/ { n[$0]++; last[$0] = NR } END { for (k in n) print last[k], k, n[k] }' \
    | sort -rn | head -n 50 | cut -d' ' -f2-
}

# dns_add PID CID NAME…: let container CID's DNS filter answer NAMEs too,
# then restart it (dnsmasq reads its server and nftset lines at start).
dns_add() {
  local pid="$1" c="$2" gw dm n old; shift 2
  local conf="$DNSDIR/$c.conf"
  gw="$(sed -n 's/^# gateway=//p' "$conf")"
  [[ "$gw" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || die "$conf names no gateway"
  # The dnsmasq the hook ran (the user's PATH may find another, one without nftset).
  dm="$(sed -n 's/^# dnsmasq=//p' "$conf")"
  [[ "$dm" == /* && -x "$dm" ]] || die "$conf names no dnsmasq: restart the container"
  for n in "$@"; do
    grep -qxF "server=/$n/$gw" "$conf" && continue
    printf 'server=/%s/%s\nnftset=/%s/4#inet#%s#allowd4\n' "$n" "$gw" "$n" "$TABLE" >>"$conf"
  done
  old="$(cat "$DNSDIR/$c.pid" 2>/dev/null)" || true
  if [[ "$old" =~ ^[0-9]+$ ]]; then
    kill -KILL "$old" 2>/dev/null || true # SIGKILL: see stop_dns
    for _ in 1 2 3 4 5 6 7 8 9 10; do kill -0 "$old" 2>/dev/null || break; sleep 0.2; done
  fi
  rm -f "$DNSDIR/$c.pid"
  in_netns "$pid" "$dm" --conf-file="$conf" </dev/null || die "the DNS filter did not restart; restart the container"
}

cmd_show() {
  # A container belongs to a worktree if it is on that worktree's dev network.
  # Names would miss railswitch exec's transient containers, which have random
  # ones.
  local svc="${1:-}"
  local net="$P-[a-z0-9-]+-dev" any=0 id name nets f profiles mode
  [[ -n "$W" ]] && net="$P-$W-dev"
  local scope="(^|[^a-z0-9_-])${net}([^a-z0-9_-]|\$)"
  # With SVC, only that service's container(s).
  local only=""
  if [[ -n "$svc" ]]; then
    [[ "$svc" =~ ^[a-z][a-z0-9-]*$ ]] || die "not a service id: $svc"
    only="^$P-[a-z0-9-]+-$svc\$"
    [[ -n "$W" ]] && only="^$P-$W-$svc\$"
  fi
  mkdir -p "$STATE"

  # State files of containers that no longer exist are noise; drop them,
  # and end a DNS filter (D182) whose poststop hook never ran.
  local live c
  live="$(podman ps -a --no-trunc -q | cut -c1-12)"
  for f in "$STATE"/*; do
    [[ -f "$f" ]] || continue
    grep -qxF "$(basename "$f")" <<<"$live" || rm -f "$f"
  done
  for f in "$STATE"/dns/*.conf; do
    [[ -f "$f" ]] || continue
    c="$(basename "$f" .conf)"
    grep -qxF "$c" <<<"$live" && continue
    stop_dns "$c"
  done

  while read -r id name nets; do
    [[ -n "$id" && "$nets" =~ $scope ]] || continue
    [[ -z "$only" || "$name" =~ $only ]] || continue
    any=1
    f="$STATE/${id:0:12}"
    if [[ ! -f "$f" ]]; then
      # A firewall = "self" service (the browser) keeps its own
      # in-container firewall and has no hook.
      if self_firewalled "$name"; then
        if [[ "$TSV" ]]; then printf 'C\t%s\tself\t\t\n' "$name"; continue; fi
        printf '\n%s  own in-container firewall\n' "$name"
      else
        if [[ "$TSV" ]]; then printf 'C\t%s\tnone\t\t\n' "$name"; continue; fi
        printf '\n%s  NO FIREWALL -- the hook did not run for this container.\n' "$name"
        printf '  Check: railswitch doctor; then recreate it (railswitch render, then restart it)\n'
      fi
      continue
    fi
    profiles="$(sed -n 's/^profiles=//p' "$f")"
    mode="$(sed -n 's/^mode=//p' "$f")"
    if [[ "$TSV" ]]; then
      printf 'C\t%s\tfirewalled\t%s\t%s\n' "$name" "$profiles" "$mode"
    else
      printf '\n%s  profiles=%s mode=%s\n' "$name" "$profiles" "$mode"
    fi
    [[ "$mode" == off ]] && continue
    local pid entries refused rname rcount
    pid="$(podman inspect -f '{{.State.Pid}}' "$id")"
    entries="$( { set_entries "$pid" blocked4; set_entries "$pid" blocked6; } | sort -u)"
    refused="$(dns_refused "${id:0:12}")"
    # Names the DNS filter refused never became packets (D182): listed apart.
    if [[ -n "$refused" ]]; then
      [[ "$TSV" ]] || printf '  refused names (DNS filter), lookups:\n'
      while read -r rname rcount; do
        if [[ "$TSV" ]]; then printf 'N\t%s\t%s\t%s\n' "$name" "$rname" "$rcount"
        else printf '    %-60s %s\n' "$rname" "$rcount"; fi
      done <<<"$refused"
    fi
    if [[ -z "$entries" ]]; then
      [[ "$TSV" || -n "$refused" ]] || printf '  nothing %s\n' "$([[ "$mode" == log ]] && echo 'would have been rejected' || echo rejected)"
      continue
    fi
    [[ "$TSV" ]] || printf '  %s:\n' "$([[ "$mode" == log ]] && echo 'would reject under enforce' || echo rejected)"
    local addr proto port host
    while read -r addr proto port; do
      host="$(timeout 2 getent hosts "$addr" 2>/dev/null | awk '{ print $2; exit }')" || true
      # A reverse name is the address owner's to choose: show it only when
      # it resolves back to the address (else Settings would offer to
      # allow a name the owner of that address picked).
      if [[ -n "$host" ]] && ! timeout 2 getent ahosts "$host" 2>/dev/null | awk '{ print $1 }' | grep -qxF "$addr"; then
        host=""
      fi
      if [[ "$TSV" ]]; then
        printf 'R\t%s\t%s\t%s\t%s\t%s\n' "$name" "$addr" "$proto" "$port" "${host:-}"
      else
        printf '    %-40s %s/%s  %s\n' "$addr" "$proto" "$port" "${host:-}"
      fi
    done <<<"$entries"
  done < <(podman ps --format '{{.ID}} {{.Names}} {{.Networks}}')

  (( any )) || [[ "$TSV" ]] || printf 'no running containers%s%s\n' "${svc:+ of service $svc}" "${W:+ for worktree $W}"
}

cmd_allow() {
  local for=""
  if [[ "${1:-}" == --for ]]; then
    for="${2:-}"; shift 2 || true
    [[ "$for" =~ ^[1-9][0-9]*s$ ]] || die "--for wants <seconds>s, e.g. 1800s"
  fi
  local svc="${1:-}"; shift || true
  [[ -n "$svc" && $# -gt 0 ]] || die "usage: railswitch fw allow <service> <host|ip>...  e.g. railswitch fw allow nvim example.com"
  [[ -n "$W" ]] || die "run from inside a worktree, or pass -w <worktree>"
  local name="$P-$W-$svc" id pid ips=() names=() h ip
  id="$(podman inspect -f '{{.Id}}' "$name" 2>/dev/null)" || die "$name is not running"
  [[ -f "$STATE/${id:0:12}" ]] || die "$name has no firewall to adjust"
  pid="$(podman inspect -f '{{.State.Pid}}' "$name")"
  for h in "$@"; do
    if [[ "$h" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}(/[0-9]{1,2})?$ ]]; then
      ips+=("$h"); continue
    fi
    [[ "$h" =~ ^[A-Za-z0-9.-]+$ ]] || die "not a hostname: $h"
    names+=("$h")
    while read -r ip; do ips+=("$ip"); done < <(getent ahostsv4 "$h" | awk '{ print $1 }' | sort -u)
    printf '%s -> %s\n' "$h" "$(getent ahostsv4 "$h" | awk '{ print $1 }' | sort -u | paste -sd' ' -)"
  done
  (( ${#ips[@]} )) || die "nothing resolved"
  # A container behind the DNS filter must also be answered for the names.
  # Not for a timed allow: the filter has no expiry, and each lookup would
  # re-add the addresses to allowd4, outlasting --for.
  if (( ${#names[@]} )) && [[ -f "$DNSDIR/${id:0:12}.conf" ]]; then
    if [[ -n "$for" ]]; then
      printf 'note: %s allowed by address only; its DNS filter does not answer %s, so code that looks the name up still fails (allow without --for for that)\n' "$name" "${names[*]}"
    else
      dns_add "$pid" "${id:0:12}" "${names[@]}"
      printf 'its DNS filter now answers %s\n' "${names[*]}"
    fi
  fi
  if [[ -n "$for" ]]; then
    local timed=() i
    for i in "${ips[@]}"; do
      [[ "$i" != */* ]] || die "--for takes addresses, not the range $i"
      timed+=("$i timeout $for")
    done
    in_netns "$pid" nft add element inet "$TABLE" allowt4 "{ $(IFS=,; printf '%s' "${timed[*]}") }" \
      || die "$name has no timed allow set: restart it to pick up the current firewall"
    printf 'allowed in %s for %s (the kernel drops it then, or at its next start)\n' "$name" "$for"
    return
  fi
  in_netns "$pid" nft add element inet "$TABLE" allow4 "{ $(IFS=,; printf '%s' "${ips[*]}") }"
  printf 'allowed in %s until its next start\n' "$name"
}

case "${1:-}" in
  show)  shift; (( $# <= 1 )) || die "usage: $(basename "$0") show [service]"; cmd_show "$@" ;;
  allow) shift; cmd_allow "$@" ;;
  *) die "usage: $(basename "$0") {show [service]|allow <service> <host|ip>...}" ;;
esac
