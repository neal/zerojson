#!/bin/bash

set -euo pipefail

# Run every fuzz target in every module. The target list is discovered at run
# time, so newly generated per-type and sequence fuzzers automatically enter
# the campaign. CI can split the stable global list into N modulo shards.
fuzztime=${FUZZTIME:-60s}
parallel=${FUZZPARALLEL:-1}
timeout=${FUZZ_TIMEOUT:-10m}
minimize_time=${FUZZMINIMIZETIME:-5s}
shard_index=${FUZZ_SHARD_INDEX:-0}
shard_count=${FUZZ_SHARD_COUNT:-1}
target_pattern=${FUZZ_TARGET_PATTERN:-.*}

if (( shard_count < 1 || shard_index < 0 || shard_index >= shard_count )); then
	echo "invalid shard: index=$shard_index count=$shard_count" >&2
	exit 2
fi

root=$(cd "$(dirname "$0")/.." && pwd)
modules=(. cmd/zerojsongen conformance)
index=0
matched=0
selected=0
failures=0
failed_targets=()

for module in "${modules[@]}"; do
	cd "$root/$module"
	packages=$(go list ./...)
	for package in $packages; do
		targets=$(go test "$package" -list '^Fuzz[A-Za-z0-9_]*$' | awk '/^Fuzz[A-Za-z0-9_]*$/ { print }')
		for target in $targets; do
			if [[ $target =~ $target_pattern ]]; then
				if (( matched % shard_count == shard_index )); then
					selected=$((selected + 1))
					echo "[$selected] module=$module package=$package target=$target fuzztime=$fuzztime"
					if ! go test "$package" -run '^$' -fuzz "^${target}$" -fuzztime "$fuzztime" -fuzzminimizetime "$minimize_time" -parallel "$parallel" -timeout "$timeout"; then
						failures=$((failures + 1))
						failed_targets+=("$module:$package:$target")
					fi
				fi
				matched=$((matched + 1))
			fi
			index=$((index + 1))
		done
	done
done

echo "fuzz campaign complete: selected=$selected matched=$matched discovered=$index failures=$failures shard=$shard_index/$shard_count target_pattern=$target_pattern"
if (( failures > 0 )); then
	printf 'failed target: %s\n' "${failed_targets[@]}" >&2
	exit 1
fi
