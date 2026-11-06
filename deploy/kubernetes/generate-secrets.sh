#!/bin/sh
# Writes the files deploy/kubernetes turns into Secrets and a ConfigMap: a
# random token for each bot, kept if it already exists, a credentials file,
# the sign-in settings and the OAuth client secrets to fill in. None of these
# files is committed.
set -eu
dir="$(dirname "$0")"
umask 077
# Earlier releases kept these files in base/ and multi-user/. Move them here
# rather than generate new ones, which would replace the live bot tokens and
# credentials on the next apply.
for moved in base/bot-tokens.env base/credentials.env multi-user/auth.env multi-user/oauth-secrets.env; do
  old="$dir/$moved"
  new="$dir/$(basename "$moved")"
  if [ -e "$old" ] && [ ! -e "$new" ]; then
    mv "$old" "$new"
    echo "moved $old to $new"
  fi
done
tokens="$dir/bot-tokens.env"
if [ ! -s "$tokens" ]; then
  for bot in bug-bot feature-bot issue-bot review-bot release-bot simplifier-bot repo-bot mayor-bot; do
    printf '%s=%s\n' "$bot" "$(head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  done > "$tokens"
  echo "wrote $tokens"
fi
credentials="$dir/credentials.env"
if [ ! -e "$credentials" ]; then
  cat > "$credentials" <<'CREDENTIALS'
# GitHub token for gh and git in Town and every bot.
GH_TOKEN=
# Add the API keys your agent harnesses need, one NAME=value per line.
CREDENTIALS
  echo "wrote $credentials; set GH_TOKEN and your agent keys in it"
fi
auth="$dir/auth.env"
if [ ! -e "$auth" ]; then
  cat > "$auth" <<'AUTH'
# Sign-in for Town. Configure GitHub, Google or both, and at least one
# allowlist rule. Lists are comma-separated. See "Sign-in" in
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
oauth="$dir/oauth-secrets.env"
if [ ! -e "$oauth" ]; then
  cat > "$oauth" <<'OAUTH'
# The OAuth client secrets, one per provider you configured in auth.env.
github-client-secret=
google-client-secret=
OAUTH
  echo "wrote $oauth; set the client secrets in it"
fi
