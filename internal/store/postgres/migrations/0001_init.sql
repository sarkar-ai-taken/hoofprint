CREATE TABLE IF NOT EXISTS agents (
    id              text PRIMARY KEY,
    cluster         text NOT NULL,
    version         text NOT NULL DEFAULT '',
    capabilities    text[] NOT NULL DEFAULT '{}',
    tags            jsonb NOT NULL DEFAULT '{}'::jsonb,
    status          text NOT NULL DEFAULT 'offline',
    registered_at   timestamptz NOT NULL DEFAULT now(),
    last_heartbeat  timestamptz
);

CREATE TABLE IF NOT EXISTS deploys (
    id                   text PRIMARY KEY,
    agent_id             text NOT NULL,
    cluster              text NOT NULL,
    namespace            text NOT NULL,
    kind                 text NOT NULL,
    name                 text NOT NULL,
    tier                 smallint NOT NULL DEFAULT 0,
    controller           text NOT NULL DEFAULT '',
    images_before        jsonb NOT NULL DEFAULT '[]'::jsonb,
    images_after         jsonb NOT NULL DEFAULT '[]'::jsonb,
    template_hash_before text NOT NULL DEFAULT '',
    template_hash_after  text NOT NULL DEFAULT '',
    revision_before      text NOT NULL DEFAULT '',
    revision_after       text NOT NULL DEFAULT '',
    labels               jsonb NOT NULL DEFAULT '{}'::jsonb,
    annotations          jsonb NOT NULL DEFAULT '{}'::jsonb,
    observed_at          timestamptz NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS deploys_observed_at_idx ON deploys (observed_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS deploys_cluster_ns_idx ON deploys (cluster, namespace);

CREATE TABLE IF NOT EXISTS verdicts (
    id          text PRIMARY KEY,
    deploy_id   text NOT NULL REFERENCES deploys(id) ON DELETE CASCADE,
    judge       text NOT NULL,
    outcome     text NOT NULL,
    confidence  double precision NOT NULL DEFAULT 0,
    rationale   text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS verdicts_deploy_idx ON verdicts (deploy_id, created_at DESC);

CREATE TABLE IF NOT EXISTS evidence (
    id          text PRIMARY KEY,
    verdict_id  text NOT NULL REFERENCES verdicts(id) ON DELETE CASCADE,
    kind        text NOT NULL,
    title       text NOT NULL DEFAULT '',
    payload     bytea NOT NULL DEFAULT ''::bytea,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS evidence_verdict_idx ON evidence (verdict_id);
