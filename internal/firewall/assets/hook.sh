#!/bin/bash
# OCI createRuntime hook: egress firewall for one worktree container, applied
# from OUTSIDE it.
#
# The container never holds NET_ADMIN, so nothing running inside -- an agent,
# code it wrote that rails hot-reloads, an npm postinstall -- can change its own
# rules. (The old agent container used to build its firewall itself through a
# sudo rule that accepted any domain as an argument, so the agent could allow
# whatever it liked.) A failing hook fails the container start: fail closed.
#
# Wiring: railswitch render writes the hook descriptor into hooks.d/ here.
# Units pass --hooks-dir there plus these annotations, and the hook fires
# only when the first is present:
#   <prefix>.firewall        comma list of profiles: none | app | deps | agent |
#                            agent-<name> (each but none names <profile>.allow
#                            here; a missing file fails the start)
#   <prefix>.firewall.mode   enforce: reject what is not allowed
#                            log:     allow everything, record what enforce
#                                     would reject (railswitch fw show)
#                            off:     no rules at all
#   <prefix>.firewall.networks  comma list of the podman networks the container
#                            joins; their subnets are what "local" means
#   <prefix>.firewall.extra  optional extra hostnames/IPs, comma or space separated
#   <prefix>.firewall.dns    filter: run a DNS filter for the container (D182,
#                            below); render sets it, and mounts resolv.conf,
#                            under enforce when the host has dnsmasq with nftset
#
# DNS filter (D182): a dnsmasq the hook starts in the container's network
# namespace (not its process namespace: nothing inside can see or signal
# it), on 127.0.0.1:53, which the container's /etc/resolv.conf names (a
# read-only mount render adds: podman's own would name the network's
# resolver). Queries to any of its networks' resolvers (each gateway) are
# rejected unless they come from the filter's own source port (QPORT), so it
# can't be skipped. It answers the allow lists' host names (and their
# subdomains), the worktree's container names and *.dns.podman, each by
# asking the network's own resolver, and puts every address it answers
# into allowd4 as it goes, with a timeout: a host whose addresses rotate
# keeps working. Every other name gets REFUSED, which closes DNS as a way
# out. The poststop hook (hook.sh <prefix> <dir> stop) ends it.
#
# crun runs hooks inside podman's rootless user namespace, where this process
# is uid 0 with CAP_NET_ADMIN over the container's network namespace, so plain
# nsenter + nft works. Hostnames are resolved here, by the host, once per
# start: no DNS reaches the container's allowlist from inside it.
#
# Never allowed, whatever an allow list says (D179): FLOOR below -- this
# network, private ranges, carrier-grade NAT and link-local, which holds the
# cloud metadata address. They are rejected after the container's own
# networks, so a LAN address or 169.254.169.254 can't be opened by an allow
# entry or a hostname that resolves to one. config.FirewallFloor is the same
# list (a test checks).
#
# Always allowed: loopback, replies, and the subnets of the networks named in
# <prefix>.firewall.networks -- the worktree's dev network (db, redis, rustfs,
# playwright, aardvark DNS at the gateway) and the proxy network (Traefik).
# They are read from netavark's config files (NETWORK_DIR, rendered into
# the hook descriptor), not from the netns: for a UserNS=keep-id container podman
# configures the network only AFTER the OCI create, so at createRuntime the
# netns has no routes yet. Known gap: aardvark-dns forwards any name upstream,
# so DNS itself remains a narrow exfil channel.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
P="${1:?usage: hook.sh <project-prefix> <firewall-dir>  (OCI state JSON on stdin)}"
ROOT="${2:?usage: hook.sh <project-prefix> <firewall-dir>  (OCI state JSON on stdin)}"
STATE="$ROOT/state"
LOG="$ROOT/hook.log"
TABLE=wt_firewall
# The DNS filter's source port for its own upstream queries. Below 1024: the
# netns keeps the kernel's default ip_unprivileged_port_start (1024) and the
# container has no capabilities, so nothing inside can bind it while
# dnsmasq doesn't (a restart, a crash); dnsmasq runs as root here and can.
QPORT=953
FLOOR=(0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16)

# Only this user reads the state and the log (another local user would
# learn every container's addresses and blocked hosts): new files 0600,
# and files an older hook left 0666 are narrowed.
umask 077
mkdir -p "$STATE"
chmod 700 "$STATE" || true
for f in "$STATE"/* "$STATE"/dns/* "$LOG"; do [[ -f "$f" ]] && chmod 600 "$f"; done 2>/dev/null || true
log() { printf '%s %s\n' "$(date '+%F %T')" "$*" >>"$LOG"; }
die() { log "error: ${cid:-?} $*"; printf 'firewall hook: %s\n' "$*" >&2; exit 1; }

oci="$(cat)"

# The OCI state is flat JSON with string annotation values; the fields needed
# are matched directly rather than requiring jq on the host.
field_num() { [[ "$oci" =~ \"$1\"[[:space:]]*:[[:space:]]*([0-9]+) ]] && printf '%s' "${BASH_REMATCH[1]}"; }
field_str() {
  local key="${1//./\\.}"
  [[ "$oci" =~ \"$key\"[[:space:]]*:[[:space:]]*\"([^\"]*)\" ]] && printf '%s' "${BASH_REMATCH[1]}"
}

cid="$(field_str id)" || die "no id in the OCI state"
cid="${cid:0:12}"
DNSDIR="$STATE/dns"

# poststop: end the container's DNS filter, if it has one. Never fails: the
# container is already gone.
if [[ "${3:-}" == stop ]]; then
  if [[ -f "$DNSDIR/$cid.pid" ]]; then
    dpid="$(cat "$DNSDIR/$cid.pid" 2>/dev/null)"
    [[ "$dpid" =~ ^[0-9]+$ ]] && kill "$dpid" 2>/dev/null || true
  fi
  rm -f "$DNSDIR/$cid.conf" "$DNSDIR/$cid.pid" "$DNSDIR/$cid.log"
  exit 0
fi

pid="$(field_num pid)" || die "no pid in the OCI state"
profiles="$(field_str "$P.firewall")" || die "no $P.firewall annotation"
mode="$(field_str "$P.firewall.mode")" || true
extra="$(field_str "$P.firewall.extra")" || true
networks="$(field_str "$P.firewall.networks")" || true
dns="$(field_str "$P.firewall.dns")" || true

case "$mode" in
  enforce|log|off) ;;
  "") die "$P.firewall.mode is empty -- the unit predates the firewall; run: railswitch render" ;;
  *) die "$P.firewall.mode=$mode; expected enforce, log or off" ;;
esac

if [[ "$mode" == off ]]; then
  printf 'profiles=%s\nmode=off\n' "$profiles" >"$STATE/$cid"
  log "$cid pid=$pid profiles=$profiles mode=off: no rules"
  exit 0
fi

files=()
IFS=, read -ra plist <<<"$profiles"
for p in "${plist[@]}"; do
  case "$p" in
    none) ;;
    app|deps|agent) files+=("$HERE/$p.allow") ;;
    # agent-<name> becomes a path: a regex test, never a case glob, so no /
    # or .. gets through.
    *) [[ "$p" =~ ^agent-[a-z][a-z0-9-]{0,24}$ ]] || die "unknown firewall profile '$p' in $P.firewall=$profiles"
       files+=("$HERE/$p.allow") ;;
  esac
done

ipv4='^[0-9]{1,3}(\.[0-9]{1,3}){3}(/[0-9]{1,2})?$'
allow=()      # IPv4 addresses and CIDRs
labels=()     # "address name" pairs for fw:show
unresolved=()

add() { allow+=("$1"); labels+=("$1 $2"); }

# GitHub and Datadog publish their ranges; resolving one hostname would miss
# most of them. Needs curl and jq on the host; without them, or offline, the
# entry is skipped and named in the log.
meta_ranges() {
  local url filter json cidr
  case "$1" in
    github)       url=https://api.github.com/meta
                  filter='(.web + .api + .git)[]' ;;
    datadog-us5)  url=https://ip-ranges.us5.datadoghq.com/api.json
                  filter='.api.prefixes_ipv4[]' ;;
    *) unresolved+=("meta:$1 (unknown)"); return 0 ;;
  esac
  if ! command -v curl >/dev/null || ! command -v jq >/dev/null; then
    unresolved+=("meta:$1 (needs curl and jq)"); return 0
  fi
  json="$(curl -fsS --max-time 15 "$url" 2>/dev/null)" || { unresolved+=("meta:$1 (fetch failed)"); return 0; }
  while read -r cidr; do
    [[ "$cidr" =~ $ipv4 ]] && add "$cidr" "meta:$1"
  done < <(jq -r "$filter" <<<"$json" 2>/dev/null)
}

names=()      # host names, for the DNS filter
resolve() {
  local name="$1" ip found=""
  [[ "$name" =~ ^[A-Za-z0-9.-]+$ ]] || { unresolved+=("$name (not a hostname)"); return 0; }
  names+=("$name")
  while read -r ip; do
    [[ "$ip" =~ $ipv4 ]] || continue
    add "$ip" "$name"; found=1
  done < <(getent ahostsv4 "$name" 2>/dev/null | awk '{ print $1 }' | sort -u)
  [[ -n "$found" ]] || unresolved+=("$name")
}

entry() {
  case "$1" in
    meta:*) meta_ranges "${1#meta:}" ;;
    *) if [[ "$1" =~ $ipv4 ]]; then add "$1" literal; else resolve "$1"; fi ;;
  esac
}

wanted=0
for f in ${files[@]+"${files[@]}"}; do
  [[ -f "$f" ]] || die "missing allowlist $f"
  while read -r line; do
    line="${line%%#*}"; line="${line//[[:space:]]/}"
    [[ -n "$line" ]] || continue
    wanted=1; entry "$line"
  done <"$f"
done
read -ra extras <<<"${extra//,/ }"
for e in ${extras[@]+"${extras[@]}"}; do wanted=1; entry "$e"; done

# A profile that wanted addresses and got none means host DNS or the network
# is down. Enforcing would start a container that cannot reach anything it
# needs, so say so instead.
if [[ "$mode" == enforce && "$wanted" == 1 && ${#allow[@]} -eq 0 ]]; then
  die "profiles=$profiles resolved to no addresses (host DNS down?): ${unresolved[*]}"
fi
# Hostnames often share addresses (rubygems.org and index.rubygems.org).
if (( ${#allow[@]} )); then
  mapfile -t allow < <(printf '%s\n' "${allow[@]}" | sort -u)
fi

# netavark's own record of each network: "subnets": [ { "subnet": "10.89.0.0/24", ...
local4=()
[[ -d "${NETWORK_DIR:-}" ]] || die "NETWORK_DIR=${NETWORK_DIR:-} is not a directory -- rerun: railswitch render"
IFS=, read -ra netlist <<<"$networks"
(( ${#netlist[@]} )) || die "no $P.firewall.networks annotation -- recreate the container from current units (railswitch render, then restart it)"
for n in "${netlist[@]}"; do
  [[ "$n" =~ ^[A-Za-z0-9_.-]+$ ]] || die "bad network name '$n'"
  [[ -f "$NETWORK_DIR/$n.json" ]] || die "network $n has no config at $NETWORK_DIR/$n.json"
  while read -r net; do
    [[ "$net" =~ $ipv4 ]] && local4+=("$net")
  done < <(grep -oE '"subnet"[[:space:]]*:[[:space:]]*"[^"]+"' "$NETWORK_DIR/$n.json" | grep -oE '[0-9.]+/[0-9]+')
done
# Each network's gateway runs a resolver (aardvark-dns) that forwards any
# name upstream: the DNS filter's rules block all of them. The first
# network's answers the worktree's container names: the filter asks it.
gw_of() { grep -oE '"gateway"[[:space:]]*:[[:space:]]*"[0-9.]+"' "$NETWORK_DIR/$1.json" | grep -oE '[0-9]+(\.[0-9]+){3}' || true; }
gateway="$(gw_of "${netlist[0]}" | head -n 1)"
gateways=()
for n in "${netlist[@]}"; do
  while read -r g; do [[ "$g" =~ $ipv4 ]] && gateways+=("$g"); done < <(gw_of "$n")
done
filter=""
if [[ "$dns" == filter && "$mode" == enforce ]]; then
  DNSMASQ="$(command -v dnsmasq)" || die "$P.firewall.dns=filter, but there is no dnsmasq on this host: install it (railswitch doctor), or rerun railswitch render"
  [[ "$gateway" =~ $ipv4 ]] || die "network ${netlist[0]} has no gateway in $NETWORK_DIR/${netlist[0]}.json: the DNS filter has nothing to ask"
  filter=1
fi

elements() { (( $# )) || return 0; printf 'elements = { %s }' "$(IFS=,; printf '%s' "$*")"; }

if [[ "$mode" == enforce ]]; then
  verdict='reject with icmpx admin-prohibited'
else
  verdict='accept'
fi
floor="$(IFS=,; printf '%s' "${FLOOR[*]}")"

ruleset="$(cat <<EOF
table inet $TABLE
delete table inet $TABLE
table inet $TABLE {
  set allow4 { type ipv4_addr; flags interval; auto-merge; $(elements ${allow[@]+"${allow[@]}"}) }
  # fw allow --for: addresses the kernel drops when their timeout ends.
  set allowt4 { type ipv4_addr; flags timeout; }
  # The DNS filter's answers (D182); each lasts an hour after it was first
  # added (a later lookup doesn't extend it).
  set allowd4 { type ipv4_addr; flags timeout; timeout 1h; }
  set local4 { type ipv4_addr; flags interval; auto-merge; $(elements ${local4[@]+"${local4[@]}"}) }
  set floor4 { type ipv4_addr; flags interval; elements = { $floor } }
  # Everything that met the final verdict: rejected under enforce, would have
  # been under log. Read by fw:show; entries age out after a week.
  set blocked4 { type ipv4_addr . inet_proto . inet_service; flags dynamic,timeout; timeout 7d; size 4096; }
  set blocked6 { type ipv6_addr . inet_proto . inet_service; flags dynamic,timeout; timeout 7d; size 4096; }
  chain output {
    type filter hook output priority filter; policy accept;
    oif "lo" accept
    ct state established,related accept
$([[ -n "$filter" ]] && for g in "${gateways[@]}"; do printf '    ip daddr %s udp dport 53 udp sport != %s goto deny\n    ip daddr %s tcp dport 53 goto deny\n' "$g" "$QPORT" "$g"; done)
    ip daddr @local4 accept
    ip daddr @floor4 goto deny
    ip daddr @allow4 accept
    ip daddr @allowt4 accept
    ip daddr @allowd4 accept
    # Neighbour discovery and other link-local IPv6 housekeeping.
    ip6 daddr { fe80::/10, ff02::/16 } accept
    goto deny
  }
  chain deny {
    meta l4proto { tcp, udp } meta nfproto ipv4 add @blocked4 { ip daddr . meta l4proto . th dport }
    meta l4proto { tcp, udp } meta nfproto ipv6 add @blocked6 { ip6 daddr . meta l4proto . th dport }
    $verdict
  }
}
EOF
)"

nsenter -t "$pid" -n nft -f - <<<"$ruleset" 2>>"$LOG" || die "nft rejected the ruleset for pid $pid (see $LOG)"

# Read the table back and check what must hold (D179): nft returning 0 is
# not proof the kernel holds these rules. Any miss fails the start.
live="$(nsenter -t "$pid" -n nft list table inet "$TABLE" 2>>"$LOG")" || die "cannot list table $TABLE in pid $pid (see $LOG)"
# block NAME: the lines of "chain NAME" up to its closing brace.
block() { awk -v h="chain $1 {" 'index($0, h) { on = 1; next } on && /^[[:space:]]*}[[:space:]]*$/ { exit } on' <<<"$live"; }
# set_text NAME: "set NAME" to its first closing brace: its elements.
set_text() { awk -v h="set $1 {" 'index($0, h) { on = 1 } on { print; if (index($0, "}")) exit }' <<<"$live"; }
last_rule() { block "$1" | sed -e 's/^[[:space:]]*//' -e '/^$/d' | tail -n 1; }
line_of() { block output | grep -nF -- "$1" | head -n 1 | cut -d: -f1; }
floor_set="$(set_text floor4)"
for f in "${FLOOR[@]}"; do
  grep -qF -- "$f" <<<"$floor_set" || die "verify: floor4 lacks $f"
done
fl="$(line_of '@floor4')"; al="$(line_of '@allow4')"; tl="$(line_of '@allowt4')"; dl="$(line_of '@allowd4')"
[[ -n "$fl" && -n "$al" && -n "$tl" && -n "$dl" && "$fl" -lt "$al" && "$fl" -lt "$tl" && "$fl" -lt "$dl" ]] || die "verify: the floor rule is not ahead of the allow rules"
[[ "$(last_rule output)" == "goto deny" ]] || die "verify: chain output does not end in goto deny"
[[ "$(last_rule deny)" == "${verdict%% *}"* ]] || die "verify: chain deny does not end in ${verdict%% *}"
if (( ${#allow[@]} )); then
  grep -q 'elements' <<<"$(set_text allow4)" || die "verify: allow4 is empty, ${#allow[@]} entries were asked for"
fi
if [[ -n "$filter" ]]; then
  ll="$(line_of '@local4')"
  for g in "${gateways[@]}"; do
    ul="$(line_of "ip daddr $g udp dport 53")"; cl="$(line_of "ip daddr $g tcp dport 53")"
    [[ -n "$ul" && -n "$cl" && -n "$ll" && "$ul" -lt "$ll" && "$cl" -lt "$ll" ]] || die "verify: DNS to $g is not kept to the filter"
  done
fi

# The DNS filter: its config, then dnsmasq in the container's network
# namespace. It daemonizes; no pid file within 5 s fails the start.
if [[ -n "$filter" ]]; then
  mkdir -p "$DNSDIR"
  conf="$DNSDIR/$cid.conf"
  {
    printf '# railswitch DNS filter for %s (D182): written by hook.sh at each start.\n' "$cid"
    printf '# gateway=%s\n' "$gateway"
    printf '%s\n' port=53 listen-address=127.0.0.1 bind-dynamic no-resolv no-hosts no-poll cache-size=0 \
      user=root group=root "query-port=$QPORT" "pid-file=$DNSDIR/$cid.pid" "log-facility=$DNSDIR/$cid.log" \
      "server=//$gateway" "server=/dns.podman/$gateway"
    for n in ${names[@]+"${names[@]}"}; do
      printf 'server=/%s/%s\nnftset=/%s/4#inet#%s#allowd4\n' "$n" "$gateway" "$n" "$TABLE"
    done
  } >"$conf"
  rm -f "$DNSDIR/$cid.pid"
  nsenter -t "$pid" -n "$DNSMASQ" --conf-file="$conf" </dev/null >>"$LOG" 2>&1 || die "dnsmasq did not start for pid $pid (see $LOG)"
  for _ in 1 2 3 4 5 6 7 8 9 10; do [[ -s "$DNSDIR/$cid.pid" ]] && break; sleep 0.5; done
  [[ -s "$DNSDIR/$cid.pid" ]] || die "dnsmasq wrote no pid file for pid $pid (see $LOG)"
fi

{
  printf 'profiles=%s\nmode=%s\n' "$profiles" "$mode"
  printf 'label %s\n' ${labels[@]+"${labels[@]}"}
} >"$STATE/$cid"
log "$cid pid=$pid profiles=$profiles mode=$mode allow=${#allow[@]} local=${local4[*]:-none} dns=$([[ -n "$filter" ]] && echo filter || echo open)${unresolved[*]:+ unresolved: ${unresolved[*]}}"
