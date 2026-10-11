# Native multicore search integration

The experimental algorithm uses the `ournotes-deck` repository at the revision
recorded in `docs/native-search-revision.txt` (or a descendant).

Build and install the local experimental library from the application root:

```sh
bash scripts/build-experimental-search.sh
```

The script uses `../ournotes-deck` next to this repository by default;
`OURNOTES_DECK_SOURCE` can point to another checkout containing that revision.
The script builds the current checkout with Cargo's lockfile and atomically
replaces `libournotes_ffi_experimental.dylib`. Local Rust changes are included.
The library is ignored by Git; the revision file records the integrated baseline.
The existing macOS packaging script includes this experimental library.

In the native settings, select the experimental algorithm. Selecting it again
imports the new library by content hash if an older version was already selected.
The default library and existing persisted engine selection are preserved.

Native search accepts 1..logical-CPU-count threads through its explicit FFI
entry points; default calls use ceil(logical-CPU-count / 2). Ordinary Live,
Power, and Skip use disjoint parallel domains and a shared exact ranking.
Deterministic Gekisou also uses domain partitioning. Certified LUCK keeps one
proof coordinator and parallelizes its 120 order simulations and bounded exact-law
refinement batches through a native Crossbeam queue. Candidate proposals remain
sequential. Outer search and inner simulation share the thread allowance.
Curve-key cache capacity is divided among workers. A three-run synthetic LUCK
refinement benchmark measured 1.58 s at one thread and 0.34 s at eight threads,
with identical results. Cache-enabled/disabled and three-LUCK-range parity tests
passed. Budget-limited runs may reach different refinement depths; only proved
rankings are marked complete.

Power/Skip reuse thread-local preparation. Live branch-and-bound targets two
partitions per thread to reduce repeated bound compilation. On the measured
8-thread synthetic 512-note/six-skill-event workload this reduced median search
time from 1.40 s to 1.13 s with identical complete results. Performance depends
on the workload; the 4-thread measurement regressed by about 2.6%.

Detailed design, benchmarks and limitations are in the Rust repository's
`docs/native-parallel-search.md`.
