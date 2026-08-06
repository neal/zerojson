#!/bin/bash

set -euo pipefail

mode=${1:-quick}
if [[ $mode != quick && $mode != full ]]; then
	echo "usage: $0 [quick|full]" >&2
	exit 2
fi

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root/conformance"

echo "zerojson synthetic shadow testing: mode=$mode"
if [[ $mode == full ]]; then
	ZEROJSON_SYNTHETIC=full go test ./codec -run '^TestSyntheticShadow(Pairwise|ThreeWise|Adversarial)$' -count=1 -timeout 20m
else
	go test ./codec -run '^TestSyntheticShadowPairwise$' -count=1 -timeout 10m
fi
