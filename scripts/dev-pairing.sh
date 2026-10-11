#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
command -v python3 >/dev/null || { echo 'Pairing test launcher requires Python 3.' >&2; exit 1; }
mkdir -p .mygo
pairing_root="$(mktemp -d "$PWD/.mygo/pairing.XXXXXX")"
chmod 700 "$pairing_root"
pairing_port="$(python3 - <<'PY'
import socket
with socket.socket() as listener:
    listener.bind(('127.0.0.1', 0))
    print(listener.getsockname()[1])
PY
)"
export OURNOTES_PAIRING_TEST_ROOT="$pairing_root"
export OURNOTES_PAIRING_TEST_PORT="$pairing_port"
export OURNOTES_DEV_STORAGE="$pairing_root/data"
dev_pid=""
cleanup() {
  trap - EXIT INT TERM
  if [[ -n "$dev_pid" ]]; then
    kill -TERM "$dev_pid" 2>/dev/null || true
    wait "$dev_pid" 2>/dev/null || true
  fi
  if [[ -f "$pairing_root/adb-path" ]]; then
    pairing_adb="$(cat "$pairing_root/adb-path")"
    if [[ -x "$pairing_adb" ]]; then
      env ADB_SERVER_SOCKET="tcp:localhost:$pairing_port" "$pairing_adb" -P "$pairing_port" kill-server >/dev/null 2>&1 || true
    fi
  fi
  # Only this invocation's private keys and generated session files are removed.
  python3 - "$pairing_root" <<'PY'
import shutil, sys
shutil.rmtree(sys.argv[1])
PY
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
printf 'Fresh pairing test · private ADB port %s\n' "$pairing_port"
bash scripts/dev.sh "$@" &
dev_pid=$!
wait "$dev_pid"
