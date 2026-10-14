-- +goose Up

-- scans(id, started_at, root) per README, plus run metadata.
CREATE TABLE scans (
    id          TEXT    PRIMARY KEY,
    started_at  TEXT    NOT NULL,
    finished_at TEXT,
    root        TEXT    NOT NULL,
    full        INTEGER NOT NULL DEFAULT 0,
    partial     INTEGER NOT NULL DEFAULT 0
);

-- entries: one row per indexed filesystem record. entry_id is stable and
-- derived from (scan_id, path) so plans can reference entries by ID.
CREATE TABLE entries (
    entry_id        TEXT    PRIMARY KEY,
    scan_id         TEXT    NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    path            TEXT    NOT NULL,
    parent          TEXT    NOT NULL DEFAULT '',
    allocated_bytes INTEGER NOT NULL DEFAULT 0,
    logical_bytes   INTEGER NOT NULL DEFAULT 0,
    mtime           TEXT,
    is_dir          INTEGER NOT NULL DEFAULT 0,
    kind            TEXT    NOT NULL,
    data_class      TEXT    NOT NULL,
    bundle_id       TEXT    NOT NULL DEFAULT '',
    dev             INTEGER NOT NULL DEFAULT 0,
    inode           INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_entries_scan_kind ON entries (scan_id, kind);
CREATE INDEX idx_entries_scan_allocated ON entries (scan_id, allocated_bytes DESC);
CREATE INDEX idx_entries_scan_data_class ON entries (scan_id, data_class);
CREATE INDEX idx_entries_path ON entries (path);

-- Inaccessible paths are a partial result, never an error (SEC-ACC-05).
CREATE TABLE inaccessible (
    scan_id TEXT NOT NULL REFERENCES scans (id) ON DELETE CASCADE,
    path    TEXT NOT NULL,
    reason  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (scan_id, path)
);

-- consents(root, tier, granted_at, revoked_at) per README (SEC-ACC-07).
CREATE TABLE consents (
    root       TEXT    PRIMARY KEY,
    tier       INTEGER NOT NULL,
    granted_at TEXT    NOT NULL,
    revoked_at TEXT
);

-- plans(plan_id, hash, created_at, expires_at, status) per README (SEC-DEL-01).
CREATE TABLE plans (
    plan_id    TEXT PRIMARY KEY,
    hash       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    status     TEXT NOT NULL
);

CREATE TABLE plan_items (
    plan_id    TEXT    NOT NULL REFERENCES plans (plan_id) ON DELETE CASCADE,
    entry_id   TEXT    NOT NULL,
    path       TEXT    NOT NULL,
    dev        INTEGER NOT NULL,
    inode      INTEGER NOT NULL,
    size       INTEGER NOT NULL,
    mtime      TEXT    NOT NULL,
    data_class TEXT    NOT NULL,
    PRIMARY KEY (plan_id, entry_id)
);

-- audit_log: append-only record of applied actions (SEC-TRN-07).
CREATE TABLE audit_log (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    at      TEXT    NOT NULL,
    action  TEXT    NOT NULL,
    path    TEXT    NOT NULL,
    size    INTEGER NOT NULL DEFAULT 0,
    plan_id TEXT    NOT NULL DEFAULT '',
    result  TEXT    NOT NULL DEFAULT '',
    detail  TEXT    NOT NULL DEFAULT ''
);

-- +goose Down

DROP TABLE audit_log;
DROP TABLE plan_items;
DROP TABLE plans;
DROP TABLE consents;
DROP TABLE inaccessible;
DROP TABLE entries;
DROP TABLE scans;
