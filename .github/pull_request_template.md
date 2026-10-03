## What and why

<!-- One change per PR. The title becomes the squash commit on main, so write it as one: "feat(status): ...", "fix(guard): ...". -->

## How it was checked

- [ ] Rebased on current `origin/main`: `go vet ./... && golangci-lint run && scripts/coverage.sh && scripts/prose.sh`
- [ ] Tried live against a dev bot (say what you ran), or not applicable
- [ ] `SPEC.md` experiments/milestones and `HANDOFF.md` updated if this ends a milestone

## Deploy notes

<!-- New env vars, a migration, an intent the portal must grant, anything foundry needs before this lands. "None" is a fine answer. -->
