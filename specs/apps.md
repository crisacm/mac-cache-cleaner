# Spec: apps

Status: implemented (Phase 1).

## Capability

`internal/apps` discovers installed applications and reports the disk space
they occupy, plus the space of their associated data.

## Requirements

- **A-1** Applications are discovered under `~/Applications` and
  `/Applications` (report-only; MCC never uninstalls apps).
- **A-2** The only file MCC reads is the bundle's `Info.plist`, capped at
  1 MiB; larger plists are skipped (SEC-ACC-02).
- **A-3** Results carry the `application` data class.
- **A-4** Sizes come from the index (`entry_id`/path lookup), never from a
  fresh recursive walk of the CLI process.
- **A-5** Associated data is estimated from the conventional per-bundle
  locations (Caches, Application Support, Containers, HTTPStorages, WebKit,
  Saved Application State).

## API

- `New(index, home, roots...) *Service`
- `(*Service) List(ctx, scanID) ([]domain.App, error)` — sorted by size desc.
- `ReadBundleInfo(appPath) (BundleInfo, error)`

## Notes

- `Index` is a consumer-defined interface with a single method,
  `EntryTotalsByPath`, satisfied by `*store.Store`.
