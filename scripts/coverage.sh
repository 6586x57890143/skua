#!/usr/bin/env bash
# Race tests plus a per-package coverage floor on internal/. A total hides a
# 0% package behind a 100% one, so every package must clear it on its own.
# cmd/ and tools/ are wiring and are not held to it.
#   coretest: the test helper itself, exercised by everyone else's tests.
set -euo pipefail
floor=85
exempt='/internal/core/coretest$'

go test ./... -race -cover | tee /dev/stderr | awk -v floor="$floor" -v exempt="$exempt" '
	/coverage:/ {
		pkg = ($1 == "ok") ? $2 : $1
		for (i = 1; i <= NF; i++) if ($i == "coverage:") pct = $(i + 1)
		if (pkg !~ /\/internal\// || pkg ~ exempt || pct !~ /^[0-9]/) next
		sub(/%/, "", pct)
		if (pct + 0 < floor) { printf "::error::%s is at %s%%, under the %d%% floor\n", pkg, pct, floor; bad = 1 }
	}
	END { exit bad }'
