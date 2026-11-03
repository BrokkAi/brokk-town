#!/bin/sh
# Waits until a demo Town reports that every bot answered a dispatch over the
# network. Its arguments are the command that prints "bt status --json" for
# that Town, such as:
#
#   scripts/demo-dispatch-check.sh docker compose exec -T bt bt status --json --demo --state-dir /data
set -eu
bots="bug feature issue review release simplifier repo hall"
deadline=$(( $(date +%s) + ${TIMEOUT:-300} ))
while :; do
  if state="$("$@" 2>/dev/null)"; then
    missing=""
    for role in $bots; do
      if ! printf '%s' "$state" | jq -e --arg role "$role" \
        '[.events[]? | select(.kind == "activity" and .from == $role and (.title | contains("answered a demo dispatch over the network")))] | length > 0' >/dev/null; then
        missing="$missing $role"
      fi
    done
    if [ -z "$missing" ]; then
      printf '%s' "$state" | jq -r '.events[] | select(.title | contains("demo dispatch")) | .title'
      echo "every bot answered over the network"
      exit 0
    fi
    errors="$(printf '%s' "$state" | jq -r '[.events[]? | select(.kind == "error") | .title] | unique | .[]')"
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    echo "bots that never answered:${missing:- (no state)}" >&2
    [ -n "${errors:-}" ] && printf 'errors:\n%s\n' "$errors" >&2
    exit 1
  fi
  sleep 5
done
