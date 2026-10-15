-- +goose Up

-- Audit log of MCP calls (SEC-MCP-08). Append-only.
CREATE TABLE mcp_calls (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    at           TEXT    NOT NULL,
    tool         TEXT    NOT NULL,
    args_summary TEXT    NOT NULL DEFAULT '',
    status       TEXT    NOT NULL DEFAULT '',
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    item_count   INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_mcp_calls_at ON mcp_calls (at DESC);

-- +goose Down

DROP TABLE mcp_calls;
