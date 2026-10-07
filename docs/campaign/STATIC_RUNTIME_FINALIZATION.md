# Static runtime finalization

## Product invariant

Rclone Nexus has exactly one supported runtime authority: the executable shipped by the installed module at `system/bin/rclone`. Runtime selection happens at package time. Runtime replacement therefore means producing and flashing a new module ZIP.

PATH, a NewFuture provider module, `RNEXUS_RCLONE_BIN`, stale activation state, candidate stores, source registries, staged updates, and rollback state cannot redirect production execution.

## Finalized architecture

The mutable runtime lifecycle has been retired from the compiled Go tree. The former activation, acquisition, source, store, manager, update, build, and runtime-state packages are no longer production substrate. Mount start/reconcile and migration evidence resolve the same bundled runtime authority through `runtimeauth`; a stale historical `activation-v1.json` has no control-plane meaning.

The product surfaces agree:

- the package owns exactly `system/bin/rclone` as its rclone-family executable;
- `runtimeauth` resolves that bundled executable only;
- boot checks `racctl runtime status --json --require-operational` without recovery, promotion, or rollback;
- `racctl runtime` is an inspection surface (`status`, `executable`, `config`), not a runtime manager;
- typed control exposes `runtime.status` but no runtime mutation/source/update operations;
- the WebUI presents a read-only Bundled Runtime status view backed only by `runtime.status`;
- packaging and release contracts require `system/bin/rclone` and bind it into `integrity.manifest.json`;
- provider modules are compatibility/migration observations, never runtime authority.

Historical campaign documents and old release evidence may describe the removed lifecycle. They are provenance, not executable product promises. Retired mutable-runtime jobs/workflows are removed from the live `.devtool.toml` graph so historical prose cannot become executable authority again.

## DevTool/EXO final seal

Static-runtime convergence is a first-class target-local DevTool campaign named `rclone_static_runtime`. It owns native jobs for product-surface regression, focused Go runtime authority, WebUI static-runtime contract, shell syntax, module contract, the full Go repository suite, package validation, and a final source/package audit.

The target exposes two native DAGs with deliberately different responsibilities. `artifact-seal` is a seven-stage repository-read-only post-apply workflow; it proves product surface, focused runtime authority, WebUI, shell/module contracts, the complete Go repository, and a source-level final audit without creating release artifacts. `final-seal` is the release workflow and adds package materialization plus package verification before its final audit.

Apply the finalization overlay through DevTool's direct transaction isolation. `direct` still uses DevTool's atomic path backup/journal and rollback policy, but avoids introducing a second temporary Git-worktree integration boundary while the overlay replaces `.devtool.toml` itself:

```sh
devtool -r "$HOME/Code/rclone-nexus" --yes apply-overlay \
  /path/to/rclone-nexus-static-runtime-finalization-devtool-overlay.zip \
  --isolation direct --keep-artifact
```

Then run the release-producing EXO seal through the repository wrapper:

```sh
cd "$HOME/Code/rclone-nexus"
./devtoolw static-runtime-finalize
```

or directly through the target-local workflow:

```sh
devtool -r "$HOME/Code/rclone-nexus" invoke final-seal \
  --target rclone_static_runtime --no-workflow-parallel
```

For a read-only EXO verification without producing the module ZIP:

```sh
devtool -r "$HOME/Code/rclone-nexus" invoke artifact-seal \
  --target rclone_static_runtime --no-workflow-parallel
```

The main `rclone_nexus` `grand-g1-source` and `release` workflows cross the target boundary through `target:rclone_static_runtime#final-seal`. This makes the singular runtime seal part of normal release convergence rather than a side campaign.

Atomic overlay validation uses five first-class DevTool TestSpecs covering the product surface, focused runtime authority, WebUI, module contract, and final source audit. It intentionally does **not** run `go test ./...` while the mutation transaction is open: repository-wide tests include unrelated asynchronous subsystems and may surface environmental/teardown flakes that must be preserved as EXO evidence, not treated as a reason to undo a correctly validated runtime migration. The complete Go repository remains a required native `go-repository` stage in both post-apply `artifact-seal` and `final-seal`. This keeps the apply/rollback barrier feature-scoped and deterministic while preserving strict repository convergence as a first-class target-local EXO gate with workflow identity, DAG scheduling, preflight/toolchain resolution, run-store evidence, execution provenance, and release convergence.

The fixed point is fail-closed: do not add a test-only or production fallback to provider/PATH runtime selection to satisfy historical gates.
