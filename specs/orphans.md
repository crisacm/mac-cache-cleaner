# Spec: orphans

Status: implemented (Phase 1).

## Capability

`internal/orphan` finds leftover data under `~/Library` whose owning
application is no longer installed, and reports a confidence level.

## Requirements

- **O-1** Every orphan carries a confidence: `high` (full reverse-DNS bundle
  ID), `medium` (team prefix), `low` (name only) — never a bare boolean
  (SEC-ORP-01).
- **O-2** `com.apple.*` and Tier 2 paths are excluded (SEC-ORP-03).
- **O-3** If an application-discovery location is inaccessible, confidence is
  degraded and the report says why (SEC-ORP-02).
- **O-4** Orphans are `app_data` and carry the "may contain user data"
  warning; `low` confidence never enters a plan without explicit selection.
- **O-5** Candidate locations and their sizes come from the index; the list is
  data-driven.

## Candidate locations

Caches, Application Support, Containers, Group Containers, HTTPStorages,
WebKit, Saved Application State, Logs, Preferences.

## API

- `New(index, policy, home) *Service`
- `(*Service) List(ctx, Options) ([]domain.Orphan, error)`
- `Options{ScanID, Installed, DiscoveryComplete, DiscoveryReason, MinConfidence}`
