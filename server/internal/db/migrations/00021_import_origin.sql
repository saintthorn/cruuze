-- +goose Up
-- Which tracker (and which part of it) an import read, as its items record
-- it: "github:acme/widgets". Lets the next import of the same origin read
-- only what changed since. Empty for imports from before this.
ALTER TABLE imports ADD COLUMN origin text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE imports DROP COLUMN origin;
