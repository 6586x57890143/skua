#!/usr/bin/env bash
# Deploy the committed HEAD to foundry. The image is built on foundry itself
# from a tarball of the tree, which is native arm64 and needs no local Docker
# or registry. Uncommitted changes are refused: what runs must be a commit.
#
#   scripts/deploy.sh            # deploy HEAD
#   HOST=other scripts/deploy.sh # another ssh alias
#
# First time on the host: create ~/skua/.env from .env.example.
set -euo pipefail
cd "$(dirname "$0")/.."

host=${HOST:-foundry}
if [ -n "$(git status --porcelain)" ]; then
  echo "refusing to deploy a dirty tree; commit first" >&2
  exit 1
fi
tag=$(git rev-parse --short=12 HEAD)

echo "building skua:$tag on $host"
git archive --format=tar HEAD | ssh "$host" "docker build -q -t skua:$tag -"

ssh "$host" "mkdir -p skua"
scp -q docker-compose.prod.yml "$host:skua/docker-compose.prod.yml"
ssh "$host" bash -s -- "$tag" <<'REMOTE'
set -euo pipefail
cd ~/skua
test -f .env || { echo "~/skua/.env is missing; copy .env.example and fill it in" >&2; exit 1; }
# Keep the tag being replaced, so rolling back is reading a file.
[ -f deployed-tag.env ] && cp deployed-tag.env previous-tag.env
echo "SKUA_IMAGE_TAG=$1" > deployed-tag.env
set -a; . ./deployed-tag.env; set +a
docker compose -f docker-compose.prod.yml up -d --remove-orphans
# Dangling layers only, older than a week, so the previous release survives.
docker image prune -f --filter "until=168h" >/dev/null
docker compose -f docker-compose.prod.yml ps
REMOTE
