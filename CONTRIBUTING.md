# Contributing

Issues and pull requests are welcome. Correctness changes should include a
regression test against an independent oracle where one exists.

## Development

This repository uses Go 1.26 and a workspace containing the runtime, generator,
and conformance modules.

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

By contributing, you agree that your contributions are licensed under the
project's license.
