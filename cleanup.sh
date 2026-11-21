#!/bin/sh
# Remove only this plugin's IPv4 2223 INPUT rule; never migrate or stop host SSH.
set -eu
umask 077
: "${PLUGIN_TMP_DIR:?}"
mkdir -p "$PLUGIN_TMP_DIR"
rm -f "$PLUGIN_TMP_DIR/ready"
write_status() {
  printf '%s\n' "$1" > "$PLUGIN_TMP_DIR/cleanup.status.tmp"
  mv -f "$PLUGIN_TMP_DIR/cleanup.status.tmp" "$PLUGIN_TMP_DIR/cleanup.status"
}
firewall_rule() {
  iptables -w 2 "$@" -p tcp --dport 2223 -m comment --comment bettermeb-sshd-2223 -j ACCEPT
}
if ! command -v iptables >/dev/null 2>&1; then
  write_status 'FAILED: iptables unavailable; firewall state unknown'
  exit 1
fi
while :; do
  rc=0
  firewall_rule -C INPUT || rc=$?
  case "$rc" in
    0)
      if ! firewall_rule -D INPUT; then
        write_status 'FAILED: could not remove plugin 2223 INPUT rule'
        exit 1
      fi
      ;;
    1) write_status CLEAN; exit 0 ;;
    *) write_status 'FAILED: could not inspect plugin 2223 INPUT rule'; exit 1 ;;
  esac
done
