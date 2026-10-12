-- +goose Up

-- Billing is gone: the server no longer charges for workspaces.
DROP TABLE workspace_billing;

-- +goose Down
CREATE TABLE workspace_billing (
    workspace_id           uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    stripe_customer_id     text NOT NULL UNIQUE,
    stripe_subscription_id text NOT NULL,
    stripe_item_id         text NOT NULL,
    status                 text NOT NULL,
    billing_interval       text NOT NULL CHECK (billing_interval IN ('month', 'year')),
    seats                  integer NOT NULL,
    current_period_end     timestamptz,
    cancel_at              timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workspace_billing_subscription_idx ON workspace_billing (stripe_subscription_id);
