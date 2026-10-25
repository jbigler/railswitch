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
#   allow SVC HOST… add hostnames/IPs to a running container's allowlist, e.g.
#                   `railswitch fw allow nvim example.com`. Live only: gone on
#                   the next start. To keep it, add it to the profile's
#                   [firewall] allow.<profile> list in project.toml.
#
# Environment (set by railswitch):
#   FIREWALL_DIR     <workspace>/.railswitch/firewall: the hook's state/ dir
#   PREFIX           the project prefix
#   WORKTREE         the current worktree's slug; empty at the workspace root
#   SELF_FIREWALLED  comma list of the service ids that run their own
#                    in-container firewall (firewall = "self": no hook)
set -euo pipefail

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

  # State files of containers that no longer exist are noise; drop them.
  local live
  live="$(podman ps -a --no-trunc -q | cut -c1-12)"
  for f in "$STATE"/*; do
    [[ -f "$f" ]] || continue
    grep -qxF "$(basename "$f")" <<<"$live" || rm -f "$f"
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
        printf '\n%s  own in-container firewall\n' "$name"
      else
        printf '\n%s  NO FIREWALL -- the hook did not run for this container.\n' "$name"
        printf '  Check: railswitch doctor; then recreate it (railswitch render, then restart it)\n'
      fi
      continue
    fi
    profiles="$(sed -n 's/^profiles=//p' "$f")"
    mode="$(sed -n 's/^mode=//p' "$f")"
    printf '\n%s  profiles=%s mode=%s\n' "$name" "$profiles" "$mode"
    [[ "$mode" == off ]] && continue
    local pid entries
    pid="$(podman inspect -f '{{.State.Pid}}' "$id")"
    entries="$( { set_entries "$pid" blocked4; set_entries "$pid" blocked6; } | sort -u)"
    if [[ -z "$entries" ]]; then
      printf '  nothing %s\n' "$([[ "$mode" == log ]] && echo 'would have been rejected' || echo rejected)"
      continue
    fi
    printf '  %s:\n' "$([[ "$mode" == log ]] && echo 'would reject under enforce' || echo rejected)"
    local addr proto port host
    while read -r addr proto port; do
      host="$(timeout 2 getent hosts "$addr" 2>/dev/null | awk '{ print $2; exit }')" || true
      printf '    %-40s %s/%s  %s\n' "$addr" "$proto" "$port" "${host:-}"
    done <<<"$entries"
  done < <(podman ps --format '{{.ID}} {{.Names}} {{.Networks}}')

  (( any )) || printf 'no running containers%s%s\n' "${svc:+ of service $svc}" "${W:+ for worktree $W}"
}

cmd_allow() {
  local svc="${1:-}"; shift || true
  [[ -n "$svc" && $# -gt 0 ]] || die "usage: railswitch fw allow <service> <host|ip>...  e.g. railswitch fw allow nvim example.com"
  [[ -n "$W" ]] || die "run from inside a worktree, or pass -w <worktree>"
  local name="$P-$W-$svc" id pid ips=() h ip
  id="$(podman inspect -f '{{.Id}}' "$name" 2>/dev/null)" || die "$name is not running"
  [[ -f "$STATE/${id:0:12}" ]] || die "$name has no firewall to adjust"
  pid="$(podman inspect -f '{{.State.Pid}}' "$name")"
  for h in "$@"; do
    if [[ "$h" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}(/[0-9]{1,2})?$ ]]; then
      ips+=("$h"); continue
    fi
    [[ "$h" =~ ^[A-Za-z0-9.-]+$ ]] || die "not a hostname: $h"
    while read -r ip; do ips+=("$ip"); done < <(getent ahostsv4 "$h" | awk '{ print $1 }' | sort -u)
    printf '%s -> %s\n' "$h" "$(getent ahostsv4 "$h" | awk '{ print $1 }' | sort -u | paste -sd' ' -)"
  done
  (( ${#ips[@]} )) || die "nothing resolved"
  in_netns "$pid" nft add element inet "$TABLE" allow4 "{ $(IFS=,; printf '%s' "${ips[*]}") }"
  printf 'allowed in %s until its next start\n' "$name"
}

case "${1:-}" in
  show)  shift; (( $# <= 1 )) || die "usage: $(basename "$0") show [service]"; cmd_show "$@" ;;
  allow) shift; cmd_allow "$@" ;;
  *) die "usage: $(basename "$0") {show [service]|allow <service> <host|ip>...}" ;;
esac
