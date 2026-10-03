#!/usr/bin/env bash
# No generated-text punctuation anywhere in the tree. Byte escapes rather than
# literal characters so this file does not trip itself, -I so the PNGs never
# match by chance. CI's prose job runs this; run it before pushing.
set -euo pipefail
em=$'\342\200\224' ell=$'\342\200\246'
ldq=$'\342\200\234' rdq=$'\342\200\235'
if grep -rnI --exclude-dir=.git -e "$em" -e "$ell" -e "$ldq" -e "$rdq" . ; then
	echo "::error::Found em dashes, ellipsis characters or curly quotes. Use plain punctuation."
	exit 1
fi
