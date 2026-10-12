-- +goose Up
-- What an import was asked to do, without its credentials or file contents,
-- so that it can be run again by giving only those.
ALTER TABLE imports ADD COLUMN request jsonb NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE imports DROP COLUMN request;
