# Spec: cache

Status: implemented (Phase 1).

## Capability

`internal/cache` is a data-driven registry of cache and app-data locations plus
a service that reports their sizes.

## Requirements

- **C-1** New locations are added as data (`Locations`), never as new code
  paths (AGENTS.md §6).
- **C-2** Each location carries its data class: developer caches and system
  caches are `regenerable_cache`; Application Support / Containers are
  `app_data`.
- **C-3** Only locations that exist on the machine are reported.
- **C-4** Sizes come from the index; missing scans report zero sizes.
- **C-5** `--app ID` filters by bundle ID, name or relative path.

## Registry (initial)

System: Caches, Logs, Application Support, Containers, Group Containers,
HTTPStorages, WebKit, Saved Application State.
Developer: Xcode DerivedData/Archives/DeviceSupport, Gradle caches, Android AVD,
npm, pnpm (two locations), Go module cache, Homebrew cache, CocoaPods, Yarn,
Cargo, Docker.

## API

- `New(index, home) *Service`
- `(*Service) List(ctx, scanID, appFilter) ([]domain.CacheEntry, error)`
- `Locations` (registry), `Resolve(home, Location) string`
