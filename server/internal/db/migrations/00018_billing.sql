-- +goose Up

-- What a workspace pays for, on a server that charges (one with Stripe
-- configured). A workspace with no row has never started a checkout and is on
-- the free plan. The row mirrors Stripe, which stays the source of truth: it
-- is rewritten from the subscription whenever Stripe reports a change.
CREATE TABLE workspace_billing (
    workspace_id           uuid PRIMARY KEY REFERENCES workspaces (id) ON DELETE CASCADE,
    stripe_customer_id     text NOT NULL UNIQUE,
    -- The latest subscription. It stays here after it ends, with its status.
    stripe_subscription_id text NOT NULL,
    -- The subscription item whose quantity is the seat count.
    stripe_item_id         text NOT NULL,
    -- Stripe's subscription status: active, past_due, canceled, ...
    status                 text NOT NULL,
    billing_interval       text NOT NULL CHECK (billing_interval IN ('month', 'year')),
    -- Seats the subscription bills. Kept equal to the workspace's admins and
    -- members by the server; guests are free.
    seats                  integer NOT NULL,
    current_period_end     timestamptz,
    -- Set once the subscription is scheduled to end.
    cancel_at              timestamptz,
    created_at             timestamptz NOT NULL DEFAULT now(),
    updated_at             timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workspace_billing_subscription_idx ON workspace_billing (stripe_subscription_id);

-- +goose Down
DROP TABLE workspace_billing;
