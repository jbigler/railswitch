#!/bin/bash
# Egress firewall for the browser container (firewall = "self": no host hook).
#
# The browser loads whatever the app and the developer point it at; with open
# egress it could fetch from, and send to, anywhere. Everything it legitimately
# talks to is in private address space: the worktree's dev and proxy networks,
# the DNS server on the network gateway, and published-port clients on the
# host. So: allow loopback + RFC 1918, reject the rest.
#
# BROWSER_ALLOW_HOSTS (optional, space-separated hostnames) adds outbound
# HTTPS to hosts the app's pages load from, e.g. a JS CDN. They are resolved
# once, here; re-run this script (restart the container) if their IPs rotate.
set -euo pipefail

table=railswitch_browser

# Own table only, so a re-run replaces it and nothing else is touched.
nft delete table inet "$table" 2>/dev/null || true
nft -f - <<EOF
table inet $table {
  set allow4 { type ipv4_addr; }

  chain input {
    type filter hook input priority 0; policy drop;
    iif lo accept
    ct state established,related accept
    ip saddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 } accept
  }

  chain forward {
    type filter hook forward priority 0; policy drop;
  }

  chain output {
    type filter hook output priority 0; policy drop;
    oif lo accept
    ct state established,related accept
    ip daddr { 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 } accept
    ip daddr @allow4 tcp dport 443 accept
    # Reject rather than drop, so the browser fails fast instead of timing out.
    meta nfproto ipv4 reject with icmp type admin-prohibited
  }
}
EOF

for domain in ${BROWSER_ALLOW_HOSTS:-}; do
  ips=$(getent ahostsv4 "$domain" | awk '{print $1}' | sort -u || true)
  if [ -z "$ips" ]; then
    echo "WARN: failed to resolve $domain, skipping"
    continue
  fi
  for ip in $ips; do
    echo "Allowing $ip:443 for $domain"
    nft add element inet "$table" allow4 "{ $ip }"
  done
done

echo "Firewall: egress restricted to loopback + private ranges"
if timeout 5 bash -c 'exec 3<>/dev/tcp/1.1.1.1/443' 2>/dev/null; then
  echo "ERROR: firewall verification failed: reached 1.1.1.1:443"
  exit 1
fi
echo "Firewall verification passed: 1.1.1.1:443 is unreachable, as expected"
