# Synthetic shadow model

The shadow campaign models synthetic wire possibilities without requiring
production captures.
Every generated document is replayed through `encoding/json` and zerojson's
plain/pooled copy/borrow entry points; successful values must agree
semantically, errors must agree by acceptance verdict, copy modes are scribbled
to expose aliases, and both encoders are compared after their shared lossy rules
such as `omitempty`.

## Dimensions

| Dimension | Covered levels |
|---|---|
| `op` value | null, empty, ASCII, escaped spelling, Unicode, lone surrogate, long string, escaped controls, and wrong number/bool/object/array types |
| `set` / `where` value | absent, null, empty object, flat object, every scalar kind, nested objects, arrays, Unicode, numeric boundaries, empty containers, and wrong string/array/number/bool types |
| member order | six placements covering forward/reverse order and unknown fields before, between, and after declared fields |
| unknown content | absent, scalar, nested object, heterogeneous array, Unicode escaped key with deeper content |
| whitespace | compact, spaces around separators, multiline tabs/newlines |
| late/duplicate member | none, repeated/overridden `op`, and empty/overridden `set` or `where` |
| declared-key spelling | literal, individually escaped `op`/`set`/`where`, or all escaped |

`TestSyntheticShadowPairwise` is the per-commit tier: 1,831 rows exhaustively
cover every pair of dimension levels. `scripts/synthetic-shadow.sh full` adds
27,786 rows covering every three-way interaction plus bounded adversarial
records: nesting depth 512, 4,096-element arrays and objects, 1 MiB
strings/whitespace, and 1,024 duplicate declared keys. Coverage is verified
independently from row generation.

Grammar-invalid bytes remain covered by the existing `Valid`, truncation,
strict-skip, decode-implies-valid, resource-scaling, and fuzz suites. This model
focuses on grammar-valid wire possibilities, including values that are invalid
for the Go schema and therefore must be rejected consistently.

## Residual risk

Pairwise and three-wise coverage cannot prove every eight-dimensional
combination. The fuzz target uses one seed for every factor level to explore
interiors continuously, while the adversarial tier covers scale boundaries.
When expanding the model, document new dimensions and levels here and update
the coverage verifier. Unmodeled schemas need separate generated-schema tests.
