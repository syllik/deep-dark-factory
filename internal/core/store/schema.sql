-- DDF store schema (spec §4.5 Tables). DDL only: the version number is the
-- Go constant SchemaVersion, written into schema_version on create.
-- Times are INTEGER Unix milliseconds UTC. Enum columns are TEXT without CHECK
-- constraints: every enum is decoded through its model Parse function.
-- Payload columns hold a JSON object carrying its ddf.<name>/v1 schema ID.

CREATE TABLE schema_version (
    version INTEGER NOT NULL
);

CREATE TABLE tasks (
    task_key         TEXT    NOT NULL PRIMARY KEY,
    forge            TEXT    NOT NULL,
    repository       TEXT    NOT NULL,
    source_kind      TEXT    NOT NULL,
    source_id        TEXT    NOT NULL,
    kind             TEXT    NOT NULL,
    parent_key       TEXT    REFERENCES tasks (task_key),
    state            TEXT    NOT NULL,
    flag_needs_owner INTEGER NOT NULL DEFAULT 0,
    flag_hold        INTEGER NOT NULL DEFAULT 0,
    flag_failed      INTEGER NOT NULL DEFAULT 0,
    risk             TEXT,
    priority         TEXT    NOT NULL DEFAULT 'normal',
    hit_stop         INTEGER NOT NULL DEFAULT 0,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    teardown_at      INTEGER,
    payload          TEXT    NOT NULL,
    UNIQUE (forge, repository, source_kind, source_id)
);

CREATE TABLE runs (
    run_id        TEXT    NOT NULL PRIMARY KEY,
    task_key      TEXT    NOT NULL REFERENCES tasks (task_key),
    role          TEXT    NOT NULL,
    purpose       TEXT    NOT NULL,
    revision      INTEGER,
    attempt       INTEGER NOT NULL,
    head_sha      TEXT,
    status        TEXT    NOT NULL,
    provider      TEXT    NOT NULL,
    failure_class TEXT,
    started_at    INTEGER NOT NULL,
    finished_at   INTEGER,
    payload       TEXT    NOT NULL
);

CREATE TABLE changes (
    task_key    TEXT    NOT NULL REFERENCES tasks (task_key),
    change_id   TEXT    NOT NULL,
    kind        TEXT    NOT NULL,
    branch      TEXT    NOT NULL,
    base_branch TEXT    NOT NULL,
    head_sha    TEXT    NOT NULL,
    revision    INTEGER NOT NULL,
    open        INTEGER NOT NULL,
    draft       INTEGER NOT NULL,
    patch_id    TEXT    NOT NULL,
    merge_sha   TEXT,
    merged_at   INTEGER,
    ci_since    INTEGER,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    payload     TEXT    NOT NULL,
    PRIMARY KEY (task_key, change_id)
);

CREATE TABLE base_merges (
    task_key        TEXT    NOT NULL REFERENCES tasks (task_key),
    change_id       TEXT    NOT NULL,
    merge_sha       TEXT    NOT NULL,
    base_sha        TEXT    NOT NULL,
    parent_head_sha TEXT    NOT NULL,
    intent_id       TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    PRIMARY KEY (task_key, change_id, merge_sha)
);

CREATE TABLE reviews (
    review_id       TEXT    NOT NULL PRIMARY KEY,
    task_key        TEXT    NOT NULL REFERENCES tasks (task_key),
    change_id       TEXT    NOT NULL,
    role            TEXT    NOT NULL,
    head_sha        TEXT    NOT NULL,
    round           INTEGER NOT NULL,
    verdict         TEXT    NOT NULL,
    blocking_count  INTEGER NOT NULL,
    findings_digest TEXT    NOT NULL,
    raise_risk      TEXT,
    sig_version     TEXT    NOT NULL,
    key_id          TEXT    NOT NULL,
    signature       TEXT    NOT NULL,
    run_id          TEXT    NOT NULL,
    created_at      INTEGER NOT NULL,
    payload         TEXT    NOT NULL
);

CREATE TABLE findings (
    review_id      TEXT    NOT NULL,
    finding_id     TEXT    NOT NULL,
    task_key       TEXT    NOT NULL REFERENCES tasks (task_key),
    role           TEXT    NOT NULL,
    head_sha       TEXT    NOT NULL,
    severity       TEXT    NOT NULL,
    category       TEXT    NOT NULL,
    security_level TEXT,
    blocking       INTEGER NOT NULL,
    prior_id       TEXT,
    routed         TEXT,
    payload        TEXT    NOT NULL,
    PRIMARY KEY (review_id, finding_id)
);

CREATE TABLE questions (
    question_id  TEXT    NOT NULL PRIMARY KEY,
    task_key     TEXT    NOT NULL REFERENCES tasks (task_key),
    category     TEXT    NOT NULL,
    asked_at     INTEGER NOT NULL,
    answered_at  INTEGER,
    answer_actor TEXT,
    payload      TEXT    NOT NULL
);

CREATE TABLE intents (
    intent_id   TEXT    NOT NULL PRIMARY KEY,
    task_key    TEXT    NOT NULL REFERENCES tasks (task_key),
    operation   TEXT    NOT NULL,
    status      TEXT    NOT NULL,
    dry_run     INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    resolved_at INTEGER,
    payload     TEXT    NOT NULL
);

CREATE INDEX intents_pending ON intents (created_at) WHERE status = 'pending';

CREATE TABLE events (
    seq      INTEGER PRIMARY KEY AUTOINCREMENT,
    task_key TEXT    REFERENCES tasks (task_key),
    type     TEXT    NOT NULL,
    at       INTEGER NOT NULL,
    payload  TEXT    NOT NULL
);

CREATE TABLE pause_reasons (
    reason  TEXT    NOT NULL PRIMARY KEY,
    set_at  INTEGER NOT NULL,
    payload TEXT    NOT NULL
);

CREATE TABLE cooling (
    entry_id    TEXT    NOT NULL PRIMARY KEY,
    until       INTEGER NOT NULL,
    notified_at INTEGER
);

CREATE TABLE flakes (
    test_id   TEXT    NOT NULL,
    task_key  TEXT    NOT NULL REFERENCES tasks (task_key),
    change_id TEXT    NOT NULL,
    head_sha  TEXT    NOT NULL,
    seen_at   INTEGER NOT NULL,
    PRIMARY KEY (test_id, task_key, change_id)
);

-- events has no UPDATE or DELETE path.
CREATE TRIGGER events_no_update BEFORE UPDATE ON events
BEGIN
    SELECT RAISE(ABORT, 'events are append-only');
END;

CREATE TRIGGER events_no_delete BEFORE DELETE ON events
BEGIN
    SELECT RAISE(ABORT, 'events are append-only');
END;

-- tasks.hit_stop is sticky: once set it is never cleared.
CREATE TRIGGER tasks_hit_stop_sticky BEFORE UPDATE OF hit_stop ON tasks
WHEN OLD.hit_stop = 1 AND NEW.hit_stop <> 1
BEGIN
    SELECT RAISE(ABORT, 'tasks.hit_stop is sticky');
END;
