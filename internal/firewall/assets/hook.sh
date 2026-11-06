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
FLOOR=(0.0.0.0/8 10.0.0.0/8 100.64.0.0/10 169.254.0.0/16 172.16.0.0/12 192.168.0.0/16)

# Only this user reads the state and the log (another local user would
# learn every container's addresses and blocked hosts): new files 0600,
# and files an older hook left 0666 are narrowed.
umask 077
mkdir -p "$STATE"
chmod 700 "$STATE" || true
chmod 600 "$STATE"/* "$LOG" 2>/dev/null || true
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

pid="$(field_num pid)" || die "no pid in the OCI state"
cid="$(field_str id)" || die "no id in the OCI state"
cid="${cid:0:12}"
profiles="$(field_str "$P.firewall")" || die "no $P.firewall annotation"
mode="$(field_str "$P.firewall.mode")" || true
extra="$(field_str "$P.firewall.extra")" || true
networks="$(field_str "$P.firewall.networks")" || true

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

# GitHub, Google and Datadog publish their ranges; resolving one hostname
# would miss most of them (Google answers each lookup from a rotating pool).
# Needs curl and jq on the host; without them, or offline, the entry is
# skipped and named in the log.
meta_ranges() {
  local url filter json cidr
  case "$1" in
    google)       google_ranges; return 0 ;;
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

# google_ranges adds Google's own service ranges: goog.json without every
# range that overlaps cloud.json, Google Cloud's customer space, which any
# server rented there lives in. Without cloud.json nothing is added, rather
# than every Google Cloud customer.
google_ranges() {
  local goog cloud cidr gl cl
  if ! command -v curl >/dev/null || ! command -v jq >/dev/null; then
    unresolved+=("meta:google (needs curl and jq)"); return 0
  fi
  goog="$(curl -fsS --max-time 15 https://www.gstatic.com/ipranges/goog.json 2>/dev/null)" || { unresolved+=("meta:google (fetch failed)"); return 0; }
  cloud="$(curl -fsS --max-time 15 https://www.gstatic.com/ipranges/cloud.json 2>/dev/null)" || { unresolved+=("meta:google (cloud.json fetch failed)"); return 0; }
  # A 200 that isn't the list (a captive portal, a new schema) parses to
  # nothing; an empty cloud list would let awk read goog's lines as cloud's.
  gl="$(jq -r '.prefixes[].ipv4Prefix // empty' <<<"$goog" 2>/dev/null)" || gl=""
  cl="$(jq -r '.prefixes[].ipv4Prefix // empty' <<<"$cloud" 2>/dev/null)" || cl=""
  [[ -n "$gl" ]] || { unresolved+=("meta:google (goog.json has no ranges)"); return 0; }
  [[ -n "$cl" ]] || { unresolved+=("meta:google (cloud.json has no ranges)"); return 0; }
  while read -r cidr; do
    [[ "$cidr" =~ $ipv4 ]] && add "$cidr" meta:google
  done < <(awk '
    function lo(c,  a, p) { split(c, a, "/"); split(a[1], p, "."); return ((p[1] * 256 + p[2]) * 256 + p[3]) * 256 + p[4] }
    function hi(c,  a) { split(c, a, "/"); return lo(c) + 2 ^ (32 - a[2]) - 1 }
    BEGIN { n = 0 }
    FNR == NR { cl[n] = lo($1); ch[n] = hi($1); n++; next }
    { s = lo($1); e = hi($1); for (i = 0; i < n; i++) if (s <= ch[i] && cl[i] <= e) next; print $1 }
  ' <(printf '%s\n' "$cl") <(printf '%s\n' "$gl"))
}

resolve() {
  local name="$1" ip found=""
  [[ "$name" =~ ^[A-Za-z0-9.-]+$ ]] || { unresolved+=("$name (not a hostname)"); return 0; }
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
    ip daddr @local4 accept
    ip daddr @floor4 goto deny
    ip daddr @allow4 accept
    ip daddr @allowt4 accept
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
  grep -qwF -- "$f" <<<"$floor_set" || die "verify: floor4 lacks $f"
done
fl="$(line_of '@floor4')"; al="$(line_of '@allow4')"; tl="$(line_of '@allowt4')"
[[ -n "$fl" && -n "$al" && -n "$tl" && "$fl" -lt "$al" && "$fl" -lt "$tl" ]] || die "verify: the floor rule is not ahead of the allow rules"
[[ "$(last_rule output)" == "goto deny" ]] || die "verify: chain output does not end in goto deny"
[[ "$(last_rule deny)" == "${verdict%% *}"* ]] || die "verify: chain deny does not end in ${verdict%% *}"
if (( ${#allow[@]} )); then
  grep -q 'elements' <<<"$(set_text allow4)" || die "verify: allow4 is empty, ${#allow[@]} entries were asked for"
fi

{
  printf 'profiles=%s\nmode=%s\n' "$profiles" "$mode"
  printf 'label %s\n' ${labels[@]+"${labels[@]}"}
} >"$STATE/$cid"
log "$cid pid=$pid profiles=$profiles mode=$mode allow=${#allow[@]} local=${local4[*]:-none}${unresolved[*]:+ unresolved: ${unresolved[*]}}"
