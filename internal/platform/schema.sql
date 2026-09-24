CREATE TABLE runs (
 id uuid PRIMARY KEY,
 idempotency_key text NOT NULL UNIQUE,
 status text NOT NULL CHECK (status IN ('queued','completed','failed')),
 mode text NOT NULL DEFAULT 'fixture' CHECK (mode = 'fixture'),
 actor text NOT NULL DEFAULT 'local-user-or-api',
 dataset_hash text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz,
 result jsonb,
 error text,
 CHECK ((status='queued' AND finished_at IS NULL AND result IS NULL)
     OR (status='completed' AND finished_at IS NOT NULL AND result IS NOT NULL)
     OR (status='failed' AND finished_at IS NOT NULL AND result IS NULL))
);
CREATE TABLE outbox (
 run_id uuid PRIMARY KEY REFERENCES runs(id),
 created_at timestamptz NOT NULL DEFAULT now(),
 published_at timestamptz
);
CREATE INDEX outbox_pending ON outbox(created_at) WHERE published_at IS NULL;
CREATE INDEX runs_created ON runs(created_at DESC);
