#!/bin/bash

set -euo pipefail

if (( $# != 1 )); then
	echo "usage: $0 /path/to/sanitized-corpus" >&2
	exit 2
fi

corpus_arg=$1
if [[ ! -d $corpus_arg ]]; then
	echo "corpus directory does not exist: $corpus_arg" >&2
	exit 2
fi
corpus=$(cd "$corpus_arg" && pwd)

root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root/conformance"

echo "zerojson shadow replay: corpus=$corpus"
ZEROJSON_CORPUS_DIR=$corpus go test ./codec -run '^TestProductionCorpusShadowReplay$' -count=1 -timeout 10m
