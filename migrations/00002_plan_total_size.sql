-- +goose Up

-- Persist the plan's total size so `apply` can show it in the confirmation
-- prompt (SEC-DEL-02).
ALTER TABLE plans ADD COLUMN total_size INTEGER NOT NULL DEFAULT 0;

-- +goose Down

ALTER TABLE plans DROP COLUMN total_size;
