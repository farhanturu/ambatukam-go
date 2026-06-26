#!/usr/bin/env bash
set -euo pipefail

# Push Ambatukam Go to GitHub.
# Requires: gh CLI authenticated (run 'gh auth login' first).
# DO NOT paste tokens into chat. Use gh auth login with browser flow.

REPO_NAME="ambatukam-go"
DESCRIPTION="Composable, idiomatic Go HTTP resilience library: retry, circuit breaker, bulkhead, rate limiter, timeout, hooks. One API, zero deps, production-grade."

if ! command -v gh >/dev/null 2>&1; then
  echo "ERROR: gh CLI not installed. Install from https://cli.github.com"
  exit 1
fi

if ! gh auth status >/dev/null 2>&1; then
  echo "ERROR: gh not authenticated. Run 'gh auth login' first."
  echo "NEVER paste tokens into chat. Use 'gh auth login' with browser flow."
  exit 1
fi

cd "$(dirname "$0")/.."

if [ ! -d .git ]; then
  git init
  git checkout -b main 2>/dev/null || git branch -M main
fi

git add .
if git diff --cached --quiet; then
  echo "Nothing to commit."
else
  git commit -m "feat: initial release of Ambatukam Go v1.0.0"
fi

git tag v1.0.0 2>/dev/null || true

if ! gh repo view "farhanturu/$REPO_NAME" >/dev/null 2>&1; then
  gh repo create "$REPO_NAME" --public \
    --description "$DESCRIPTION" \
    --source=. --remote=origin --push
  git push origin v1.0.0 2>/dev/null || true
else
  if ! git remote get-url origin >/dev/null 2>&1; then
    git remote add origin "git@github.com:farhanturu/$REPO_NAME.git"
  fi
  git push -u origin main
  git push origin v1.0.0 2>/dev/null || true
fi

cat <<EOF

✓ Pushed to https://github.com/farhanturu/$REPO_NAME

Next steps (manual):
  1. Visit the repo page and enable GitHub Pages if you want a docs site.
  2. Add repo topics: go, golang, http-client, resilience, retry, circuit-breaker, rate-limiting
  3. Submit to awesome-go: https://github.com/avelino/awesome-go/blob/master/CONTRIBUTING.md
  4. Post on r/golang + Hacker News (Show HN) with the new branding.
EOF
