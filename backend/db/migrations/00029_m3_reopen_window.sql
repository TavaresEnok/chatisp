-- +goose Up
-- Per-inbox automatic reopen window (X hours). 0 keeps the previous behaviour:
-- a customer message after closure opens a new attendance and protocol.
ALTER TABLE inboxes
    ADD COLUMN reopen_window_hours integer NOT NULL DEFAULT 0,
    ADD CONSTRAINT inboxes_reopen_window_chk CHECK (reopen_window_hours BETWEEN 0 AND 720);

-- +goose Down
ALTER TABLE inboxes
    DROP CONSTRAINT inboxes_reopen_window_chk,
    DROP COLUMN reopen_window_hours;
