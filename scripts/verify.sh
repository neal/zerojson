#!/bin/bash

set -euo pipefail

mode=${1:-quick}
if [[ $mode != quick && $mode != race && $mode != full ]]; then
	echo "usage: $0 [quick|race|full]" >&2
	exit 2
fi

root=$(cd "$(dirname "$0")/.." && pwd)
modules=(. cmd/zerojsongen conformance)

# Wall-clock scaling checks are intentionally excluded from ordinary tests:
# loaded hosts can distort sub-millisecond ratios enough to create false
# regressions. The full local release gate opts into them explicitly.
if [[ $mode == full ]]; then
	export ZEROJSON_LINEARITY=1
fi

run_modules() {
	local label=$1
	shift
	for module in "${modules[@]}"; do
		echo "zerojson verify: $label module=$module"
		(
			cd "$root/$module"
			"$@"
		)
	done
}

run_checkptr_modules() {
	for module in "${modules[@]}"; do
		echo "zerojson verify: checkptr module=$module"
		(
			cd "$root/$module"
			ZEROJSON_CHECKPTR=1 go test -gcflags=all=-d=checkptr=2 ./... -count=1
		)
	done
}

# run_nounsafe exercises the provably-unsafe-free root-module build
# (`-tags nounsafe`). Asserts the build+tests stay green AND that the unsafe
# implementation file is genuinely excluded from the compiled fileset.
run_nounsafe() {
	echo "zerojson verify: nounsafe root-module build+test"
	(
		cd "$root"
		go build -tags nounsafe ./...
		go test -tags nounsafe -race ./... -count=1
		if go list -tags nounsafe -f '{{.GoFiles}}' . | tr ' ' '\n' | grep -qx unsafe_on.go; then
			echo "nounsafe build unexpectedly includes unsafe_on.go" >&2
			exit 1
		fi
	)
}

run_modules test go test ./... -count=1
run_modules vet go vet ./...

if [[ $mode == race || $mode == full ]]; then
	run_modules race go test -race ./... -count=1
	run_checkptr_modules
	run_nounsafe
fi

if [[ $mode == full ]]; then
	echo "zerojson verify: regenerate committed fixtures"
	(
		cd "$root/conformance"
		go generate ./codec ./generator/compat ./generator/typeshape ./ownership ./generator/xpkg/inner ./generator/xpkg
	)
	git -C "$root" diff --exit-code -- \
		conformance/codec/easyjson_oracle_easyjson.go \
		conformance/codec/zerojson_gen.go \
		conformance/codec/zerojson_gen_fuzz_test.go \
		conformance/generator/compat/zerojson_gen.go \
		conformance/generator/typeshape/matrix_cases_test.go \
		conformance/generator/typeshape/matrix_gen.go \
		conformance/generator/typeshape/oracle_gen.go \
		conformance/generator/typeshape/oracle_gen_easyjson.go \
		conformance/generator/typeshape/zerojson_gen.go \
		conformance/ownership/zerojson_gen.go \
		conformance/generator/xpkg/inner/zerojson_gen.go \
		conformance/generator/xpkg/inner/zerojson_gen_fuzz_test.go \
		conformance/generator/xpkg/zerojson_gen.go \
		conformance/generator/xpkg/zerojson_gen_fuzz_test.go

	echo "zerojson verify: exhaustive runtime domains"
	(
		cd "$root/conformance"
		go test -tags exhaustive -timeout 30m ./runtime -count=1
	)

	"$root/scripts/synthetic-shadow.sh" full
	"$root/scripts/mutation-test.sh" full
fi

echo "zerojson verify complete: mode=$mode"
