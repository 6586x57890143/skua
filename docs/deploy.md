# Deploy

Merging to `main` builds an image for the host's platform to GHCR and deploys it to the host with
Docker Compose, once the repository variable `DEPLOY_ENABLED` is `true`.
`scripts/deploy.sh` is the manual path.
