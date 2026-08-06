#!/bin/bash

set -euo pipefail

mode=${1:-quick}
if [[ $mode != quick && $mode != full ]]; then
	echo "usage: $0 [quick|full]" >&2
	exit 2
fi

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root/conformance"

echo "zerojson mutation testing: mode=$mode"
ZEROJSON_MUTATION=$mode go test . -run '^TestMutationDetectors$' -count=1 -v -timeout 20m
