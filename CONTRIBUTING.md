# Contributing

Issues and pull requests are welcome. Correctness changes should include a
regression test against an independent oracle where one exists.

## Development

The workspace contains the runtime, generator, and conformance modules and
requires Go 1.26 or later. CI tests Go 1.26.x and 1.27.x because their
`encoding/json` implementations differ. The suite probes behavior through
`conformance/internal/oracle`; investigate a new mismatch before changing an
allowlist. See the [toolchain compatibility guide](docs/compatibility.md#which-encodingjson-go-127-and-later).

### Repository layout

| Module | Purpose |
|---|---|
| Root | Dependency-free runtime |
| `cmd/zerojsongen` | Generator and its tooling dependencies |
| `conformance` | Tests using external oracles and generated fixtures |

`go.work` connects these three modules for repository development. Consumers
do not need it: add the runtime as a dependency and the generator with
`go get -tool`. Tool dependencies are not linked into the application.

Public docs are split by audience: the README introduces the library,
`docs/guide.md` explains generation and usage, `docs/compatibility.md` records
behavioral differences, and GoDoc owns runtime API contracts. Keep essential
validation and lifetime warnings visible in the README; link to reference
details instead of copying them there.

The generator tests create temporary packages under `conformance/` (ignored
via `.gitignore`), so do not run two `go test ./...` invocations in that
module concurrently: each sees the other's packages appear and vanish.

Run the ordinary development gate from the repository root:

```sh
scripts/verify.sh quick
```

Before proposing a release or a change to parsing, ownership, pooling, unsafe
code, or generated output, run:

```sh
scripts/verify.sh full
```

Generated conformance files are committed. After changing the generator or its
fixtures, regenerate them with:

```sh
cd conformance
go generate ./codec ./generator/compat ./generator/typeshape
```

The resulting tree must be clean after a second regeneration.

## Changes

- Keep the root runtime module dependency-free.
- Preserve the documented `encoding/json` and easyjson compatibility contract.
- Add a focused regression seed for every parser or generator bug.
- Add new runtime kernels and their boundary strategy to
  `conformance/DOMAINS.md`.
- Keep benchmarks reproducible and avoid performance claims from noisy single
  runs.

## Commit messages

Use an imperative subject without a trailing period. Aim for 50 characters
and keep it within 72. Separate the body with a blank line and hard-wrap
prose at 72 characters; leave long URLs and identifiers intact when needed.
Explain why the change is needed, important behavior changes, and tradeoffs
rather than listing files. No `feat:` or `fix:` prefix is required.

## Test organization

- Root tests cover the dependency-free runtime; tests requiring external
  oracles belong in `conformance/runtime`.
- `cmd/zerojsongen` holds generator unit tests. `conformance/generator` builds
  and runs generated packages; reusable fixture source belongs in its
  `testdata` directory.
- `conformance/codec` tests committed generated codecs: wire semantics,
  ownership, pooling, fuzzing, and benchmarks.
- Name files for the behavior they cover, not the review or incident that
  introduced them. Add a regression beside related tests; split mixed files
  when their concerns are independently useful.
- When renaming a test or fixture, update mutation selectors and script/doc
  references. Keep fuzz target names and their `testdata/fuzz` directories
  together so saved regressions continue to run.

By contributing, you agree that your contributions are licensed under the
project's license.
