#!/bin/sh
# Writes the files deploy/kubernetes/base turns into Secrets: a random token
# for each bot, kept if it already exists, and a credentials file to fill in.
# Neither file is committed.
set -eu
base="$(dirname "$0")/base"
tokens="$base/bot-tokens.env"
umask 077
if [ ! -s "$tokens" ]; then
  for bot in bug-bot feature-bot issue-bot review-bot release-bot simplifier-bot repo-bot mayor-bot; do
    printf '%s=%s\n' "$bot" "$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  done > "$tokens"
  echo "wrote $tokens"
fi
credentials="$base/credentials.env"
if [ ! -e "$credentials" ]; then
  cat > "$credentials" <<'CREDENTIALS'
# GitHub token for gh and git in Town and every bot.
GH_TOKEN=
# Add the API keys your agent harnesses need, one NAME=value per line.
CREDENTIALS
  echo "wrote $credentials; set GH_TOKEN and your agent keys in it"
fi
