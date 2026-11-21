#!/bin/sh
# Standalone plugin-owned SSH server; existing system SSH services are untouched.
set -eu
umask 077
: "${PLUGIN_DATA_DIR:?}" "${PLUGIN_TMP_DIR:?}" "${PLUGIN_DIR:?}"

port=2223
host_key=$PLUGIN_DATA_DIR/ssh_host_ed25519_key
authorized_keys=$PLUGIN_DATA_DIR/authorized_keys
sshd_bin=$(command -v sshd) || { echo 'sshd not found' >&2; exit 1; }
mkdir -p "$PLUGIN_DATA_DIR" "$PLUGIN_TMP_DIR"
chmod 700 "$PLUGIN_DATA_DIR"
rm -f "$PLUGIN_TMP_DIR/ready" "$PLUGIN_TMP_DIR/cleanup.status"
sshd_pid=''
cleanup() {
  trap '' TERM INT
  rm -f "$PLUGIN_TMP_DIR/ready" "$PLUGIN_TMP_DIR/ready.tmp"
  if [ -n "$sshd_pid" ]; then
    kill "$sshd_pid" 2>/dev/null || :
    wait "$sshd_pid" 2>/dev/null || :
  fi
  /bin/sh "$PLUGIN_DIR/cleanup.sh" || echo 'sshd firewall cleanup failed' >&2
}
trap cleanup 0
trap 'exit 0' TERM INT

if [ ! -e "$host_key" ]; then
  (
    key_tmp=$(mktemp -d "$PLUGIN_DATA_DIR/.host-key.XXXXXX")
    trap 'rm -f "$key_tmp/key" "$key_tmp/key.pub"; rmdir "$key_tmp"' 0
    trap 'exit 1' TERM INT
    ssh-keygen -q -t ed25519 -N '' -f "$key_tmp/key"
    ln "$key_tmp/key" "$host_key" || [ -f "$host_key" ]
  )
fi
chmod 600 "$host_key"
# No public key is imported from the host or a particular deployment.
if [ ! -e "$authorized_keys" ]; then
  (set -C; : > "$authorized_keys") || [ -f "$authorized_keys" ]
fi
chmod 600 "$authorized_keys"

set -- -4 -f /dev/null -h "$host_key" -p "$port" \
  -o PidFile=none -o "AuthorizedKeysFile=$authorized_keys" \
  -o AllowUsers=root -o PermitRootLogin=yes \
  -o PubkeyAuthentication=yes -o AuthenticationMethods=any \
  -o PasswordAuthentication=yes -o PermitEmptyPasswords=yes \
  -o KbdInteractiveAuthentication=no -o UsePAM=no
"$sshd_bin" -t -e "$@"

firewall_rule() {
  iptables -w 2 "$@" -p tcp --dport "$port" -m comment --comment bettermeb-sshd-2223 -j ACCEPT
}
rc=0
firewall_rule -C INPUT || rc=$?
case "$rc" in
  0) ;;
  1) firewall_rule -I INPUT 1 ;;
  *) echo 'could not inspect sshd firewall rule' >&2; exit 1 ;;
esac
"$sshd_bin" -D -e "$@" &
sshd_pid=$!

# Confirm this child owns the IPv4 listener, rather than accepting an unrelated instance.
listening() {
  for fd in /proc/"$sshd_pid"/fd/*; do
    socket=$(readlink "$fd" 2>/dev/null || :)
    case "$socket" in
      socket:\[*\]) inode=${socket#socket:[}; inode=${inode%]} ;;
      *) continue ;;
    esac
    if awk -v inode="$inode" '$2 ~ /:08AF$/ && $4 == "0A" && $10 == inode { found=1 } END { exit !found }' /proc/net/tcp; then
      return 0
    fi
  done
  return 1
}
if sleep 0.1 2>/dev/null; then step=0.1; attempts=100; else step=1; attempts=10; fi
while ! listening; do
  kill -0 "$sshd_pid" 2>/dev/null || { echo 'sshd exited before listening' >&2; exit 1; }
  attempts=$((attempts - 1))
  [ "$attempts" -gt 0 ] || { echo 'sshd listener did not become ready' >&2; exit 1; }
  sleep "$step"
done
printf 'READY pid=%s port=%s generation=%s\n' "$sshd_pid" "$port" "${PLUGIN_GENERATION:-0}" > "$PLUGIN_TMP_DIR/ready.tmp"
mv -f "$PLUGIN_TMP_DIR/ready.tmp" "$PLUGIN_TMP_DIR/ready"
status=0
wait "$sshd_pid" || status=$?
sshd_pid=''
exit "$status"
