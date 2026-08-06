#!/bin/bash

set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
count=${BENCH_COUNT:-10}
benchtime=${BENCH_TIME:-750ms}
cpus=${BENCH_CPUS:-1,8,32}
gogc=${BENCH_GOGC:-100}
order=${BENCH_BACKENDS:-"bounded unique"}
stamp=$(date -u +%Y%m%dT%H%M%SZ)
out=${BENCH_OUT:-${TMPDIR:-/tmp}/zerojson-intern-bench-$stamp}
work=$(mktemp -d "${TMPDIR:-/tmp}/zerojson-intern-work.XXXXXX")

cleanup() {
	rm -rf "$work"
}
trap cleanup EXIT

mkdir -p "$out"

for backend in bounded unique; do
	mkdir -p "$work/$backend"
	rsync -a \
		--exclude '.git' \
		--exclude '.benchmarks' \
		--exclude '/zerojsongen' \
		--exclude '/cmd/zerojsongen/zerojsongen' \
		--exclude '*.test' \
		"$root/" "$work/$backend/"
done

cp "$root/scripts/bench-intern/unique.go.txt" "$work/unique/intern.go"
# The bounded-policy unit tests intentionally inspect internTable internals.
# They are irrelevant to an isolated benchmark candidate and cannot compile
# against the temporary unique implementation. Comparative benchmarks live in
# the external zerojson_test package and remain present.
rm "$work/unique/intern_test.go"

write_metadata() {
	{
		echo "commit=$(git -C "$root" rev-parse HEAD)"
		echo "dirty=$(if [[ -n $(git -C "$root" status --porcelain) ]]; then echo yes; else echo no; fi)"
		echo "go=$(go version)"
		echo "host=$(uname -a)"
		echo "count=$count"
		echo "benchtime=$benchtime"
		echo "cpus=$cpus"
		echo "gogc=$gogc"
		echo "backend_order=$order"
		if command -v sysctl >/dev/null 2>&1; then
			sysctl -n machdep.cpu.brand_string 2>/dev/null || true
		elif command -v lscpu >/dev/null 2>&1; then
			lscpu
		fi
	} >"$out/metadata.txt"
}

run_timed() {
	local stdout=$1
	local resources=$2
	shift 2
	if [[ $(uname -s) == Darwin ]]; then
		/usr/bin/time -l "$@" >"$stdout" 2>"$resources"
	elif /usr/bin/time --version >/dev/null 2>&1; then
		/usr/bin/time -v "$@" >"$stdout" 2>"$resources"
	else
		"$@" >"$stdout" 2>"$resources"
	fi
}

run_matrix() {
	local backend=$1
	local suite=$2
	local binary=$3
	local output=$out/$backend-$suite.txt
	local resources=$out/$backend-$suite-resources.txt
	local patterns

	case "$suite" in
	kernel)
		patterns='BenchmarkInternHotHit BenchmarkInternHotHitParallel BenchmarkInternMixedParallel BenchmarkInternCardinality BenchmarkInternHotSetGCEpoch BenchmarkInternPostCardinalityGCEpoch'
		;;
	decode)
		patterns='BenchmarkDecodeInternedString_ZJSON_PooledBorrow BenchmarkDecodeInternedString_ZJSON_PooledBorrowParallel BenchmarkDecodeInternedMapKey_ZJSON_PooledBorrow BenchmarkDecodeInternedMapKey_ZJSON_PooledBorrowParallel'
		;;
	*)
		echo "unknown benchmark suite: $suite" >&2
		return 2
		;;
	esac

	: >"$output"
	: >"$resources"
	local old_ifs=$IFS
	IFS=','
	read -ra cpu_values <<<"$cpus"
	IFS=$old_ifs

	local pattern cpu sample tmp_stdout tmp_resources
	for pattern in $patterns; do
		for cpu in "${cpu_values[@]}"; do
			echo "zerojson intern benchmark: backend=$backend suite=$suite target=$pattern cpu=$cpu"
			for sample in $(seq 1 "$count"); do
				tmp_stdout=$out/.$backend-$suite-$pattern-$cpu-$sample.stdout
				tmp_resources=$out/.$backend-$suite-$pattern-$cpu-$sample.resources
				if ! run_timed "$tmp_stdout" "$tmp_resources" \
					env GOGC="$gogc" "$binary" -test.run '^$' -test.bench "^$pattern$" \
					-test.benchmem -test.count=1 -test.benchtime="$benchtime" -test.cpu="$cpu"; then
					echo "benchmark failed; diagnostics: $tmp_stdout $tmp_resources" >&2
					return 1
				fi
				cat "$tmp_stdout" >>"$output"
				{
					echo "target=$pattern cpu=$cpu sample=$sample"
					cat "$tmp_resources"
				} >>"$resources"
				rm "$tmp_stdout" "$tmp_resources"
			done
		done
	done
}

run_backend() {
	local backend=$1
	local tree=$work/$backend
	local kernel_binary=$work/$backend-kernel.test
	local decode_binary=$work/$backend-decode.test

	echo "zerojson intern benchmark: backend=$backend contract"
	(
		cd "$tree"
		ZEROJSON_CHECKPTR= go test -run '^TestIntern(ValueContract|HitPathZeroAlloc)$' -count=1 .
		go test -c -o "$kernel_binary" .
	)
	(
		cd "$tree/conformance"
		go test -c -o "$decode_binary" ./codec
	)

	run_matrix "$backend" kernel "$kernel_binary"
	run_matrix "$backend" decode "$decode_binary"
}

find_benchstat() {
	if command -v benchstat >/dev/null 2>&1; then
		command -v benchstat
		return
	fi
	if [[ -x ${HOME:-}/go/bin/benchstat ]]; then
		echo "$HOME/go/bin/benchstat"
		return
	fi
	return 1
}

write_metadata
for backend in $order; do
	case "$backend" in
	bounded | unique) run_backend "$backend" ;;
	*)
		echo "unknown backend in BENCH_BACKENDS: $backend" >&2
		exit 2
		;;
	esac
done

if benchstat=$(find_benchstat); then
	"$benchstat" "$out/bounded-kernel.txt" "$out/unique-kernel.txt" >"$out/kernel-benchstat.txt"
	"$benchstat" "$out/bounded-decode.txt" "$out/unique-decode.txt" >"$out/decode-benchstat.txt"
	cat "$out/kernel-benchstat.txt"
	cat "$out/decode-benchstat.txt"
else
	echo "benchstat not found; raw results are ready for comparison" >&2
fi

echo "zerojson intern benchmark complete: $out"
