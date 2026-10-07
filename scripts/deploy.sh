#!/usr/bin/env bash
# Manual deploy, for when CI is not the route (a hotfix with GitHub down, a
# branch you want live without merging). The normal path is merging to main.
#
# Builds the committed HEAD on foundry itself from `git archive` (native
# arm64, no local Docker, no registry), tags it exactly as CI would, and runs
# it from the same directory CI deploys to, so the next CI deploy picks up
# from a consistent state. Refuses a dirty tree: what runs is a commit.
#
#   scripts/deploy.sh                      # HEAD to foundry-deploy
#   HOST=other-alias scripts/deploy.sh
set -euo pipefail
cd "$(dirname "$0")/.."

host=${HOST:-foundry-deploy}
if [ -n "$(git status --porcelain)" ]; then
  echo "refusing to deploy a dirty tree; commit first" >&2
  exit 1
fi
sha=$(git rev-parse HEAD)
image=ghcr.io/6586x57890143/skua:$sha

echo "building $image on $host"
git archive --format=tar HEAD | ssh "$host" "docker build -q --build-arg REVISION=$sha -t $image -"

ssh "$host" "mkdir -p skua"
scp -q docker-compose.prod.yml "$host:skua/docker-compose.prod.yml"
ssh "$host" bash -s -- "$sha" <<'REMOTE'
set -euo pipefail
cd ~/skua
test -f .env || { echo "~/skua/.env is missing; run go run ./tools/setup -host <alias> -no-deploy first" >&2; exit 1; }
export SKUA_IMAGE_TAG="$1"
docker compose -f docker-compose.prod.yml up -d --remove-orphans
# Rolled back if it does not stay up for 30s, as in CI.
sleep 30
state=$(docker inspect -f '{{.State.Status}} {{.RestartCount}}' "$(docker compose -f docker-compose.prod.yml ps -aq bot)")
if [ "$state" != "running 0" ]; then
  echo "skua $1 did not stay up ($state)" >&2
  docker compose -f docker-compose.prod.yml logs --tail 30 bot >&2
  if [ -f deployed-tag.env ]; then
    set -a; . ./deployed-tag.env; set +a
    docker compose -f docker-compose.prod.yml up -d bot
    echo "rolled back to $SKUA_IMAGE_TAG" >&2
  fi
  exit 1
fi
# Only after it is running, as in CI.
[ -f deployed-tag.env ] && cp deployed-tag.env previous-tag.env
echo "SKUA_IMAGE_TAG=$1" > deployed-tag.env
docker compose -f docker-compose.prod.yml ps
REMOTE
