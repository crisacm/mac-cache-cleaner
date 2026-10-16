-- +goose Up

-- Single-generation index: `entries` holds ONE current view of the disk keyed
-- by path, instead of one copy of the tree per scan. This removes the per-scan
-- rewrite and the cascade delete of the previous generation, which dominated
-- repeated scans. `entry_id` stays an opaque handle.

CREATE TABLE entries_new (
    path            TEXT    PRIMARY KEY,
    entry_id        TEXT    NOT NULL,
    scan_id         TEXT    NOT NULL DEFAULT '',
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

-- Keep the newest completed scan's rows as the current generation, so an
-- upgrade does not force an immediate rescan.
INSERT INTO entries_new
    (path, entry_id, scan_id, parent, allocated_bytes, logical_bytes, mtime,
     is_dir, kind, data_class, bundle_id, dev, inode)
SELECT path, entry_id, scan_id, parent, allocated_bytes, logical_bytes, mtime,
       is_dir, kind, data_class, bundle_id, dev, inode
FROM entries
WHERE scan_id = (
    SELECT id FROM scans
    WHERE finished_at IS NOT NULL AND finished_at <> ''
    ORDER BY started_at DESC, id DESC LIMIT 1
);

DROP TABLE entries;
ALTER TABLE entries_new RENAME TO entries;

CREATE UNIQUE INDEX idx_entries_entry_id ON entries (entry_id);
CREATE INDEX idx_entries_parent ON entries (parent);
CREATE INDEX idx_entries_kind ON entries (kind);
CREATE INDEX idx_entries_allocated ON entries (allocated_bytes DESC);
CREATE INDEX idx_entries_data_class ON entries (data_class);

-- Inaccessible paths are also single-generation now.
CREATE TABLE inaccessible_new (
    path   TEXT NOT NULL PRIMARY KEY,
    reason TEXT NOT NULL DEFAULT ''
);

INSERT INTO inaccessible_new (path, reason)
SELECT path, reason FROM inaccessible
WHERE scan_id = (
    SELECT id FROM scans
    WHERE finished_at IS NOT NULL AND finished_at <> ''
    ORDER BY started_at DESC, id DESC LIMIT 1
);

DROP TABLE inaccessible;
ALTER TABLE inaccessible_new RENAME TO inaccessible;

-- +goose Down

DROP TABLE inaccessible;
CREATE TABLE inaccessible (
    scan_id TEXT NOT NULL,
    path    TEXT NOT NULL,
    reason  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (scan_id, path)
);

DROP TABLE entries;
CREATE TABLE entries (
    entry_id        TEXT    PRIMARY KEY,
    scan_id         TEXT    NOT NULL,
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
