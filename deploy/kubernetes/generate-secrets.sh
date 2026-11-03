#!/bin/sh
# Writes the files deploy/kubernetes/base turns into Secrets: a random token
# for each bot, kept if it already exists, and a credentials file to fill in.
# It also writes the multi-user overlay's sign-in settings and OAuth secrets
# to fill in. None of these files is committed.
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
multi="$(dirname "$0")/multi-user"
auth="$multi/auth.env"
if [ ! -e "$auth" ]; then
  cat > "$auth" <<'AUTH'
# Sign-in for a shared Town. Configure GitHub, Google or both, and at least one
# allowlist rule. Lists are comma-separated. See "Shared Town" in
# docs/operations.md.
#
# GitHub OAuth app: callback URL https://town.example.com/auth/callback/github
#BROKK_TOWN_GITHUB_CLIENT_ID=
#BROKK_TOWN_GITHUB_CLIENT_SECRET_FILE=/run/secrets/brokk-oauth/github-client-secret
# Google OAuth client: redirect URI https://town.example.com/auth/callback/google
#BROKK_TOWN_GOOGLE_CLIENT_ID=
#BROKK_TOWN_GOOGLE_CLIENT_SECRET_FILE=/run/secrets/brokk-oauth/google-client-secret
#
#BROKK_TOWN_ALLOW_GITHUB_ORGS=
#BROKK_TOWN_ALLOW_GITHUB_USERS=
#BROKK_TOWN_ALLOW_GOOGLE_DOMAINS=
#BROKK_TOWN_ALLOW_EMAILS=
AUTH
  echo "wrote $auth; set the OAuth client IDs and the allowlist in it"
fi
oauth="$multi/oauth-secrets.env"
if [ ! -e "$oauth" ]; then
  cat > "$oauth" <<'OAUTH'
# The OAuth client secrets, one per provider you configured in auth.env.
github-client-secret=
google-client-secret=
OAUTH
  echo "wrote $oauth; set the client secrets in it"
fi
