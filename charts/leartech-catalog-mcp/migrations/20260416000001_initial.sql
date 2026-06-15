-- +goose Up
-- +goose StatementBegin
-- Initial placeholder migration. Replace with your real schema.
-- NOTE: keep goose annotation tokens out of comment text — goose treats any
-- line containing the annotation marker as a real annotation and errors.
CREATE TABLE IF NOT EXISTS example (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS example;
-- +goose StatementEnd
