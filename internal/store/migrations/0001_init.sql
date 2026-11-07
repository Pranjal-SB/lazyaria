CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE jobs (
  id            TEXT PRIMARY KEY,
  kind          TEXT NOT NULL,
  engine        TEXT NOT NULL DEFAULT 'aria2',
  engine_ref    TEXT,
  state         TEXT NOT NULL,
  error_kind    TEXT, error_code INTEGER, error_msg TEXT,
  name          TEXT NOT NULL,
  uris          TEXT NOT NULL,
  torrent       BLOB,
  dir           TEXT NOT NULL, out TEXT,
  dir_dev       INTEGER,
  total_bytes   INTEGER, done_bytes INTEGER,
  options       TEXT NOT NULL DEFAULT '{}',
  limit_down    INTEGER NOT NULL DEFAULT 0, limit_up INTEGER NOT NULL DEFAULT 0,
  category      TEXT, queue TEXT NOT NULL DEFAULT 'main', priority INTEGER NOT NULL DEFAULT 0,
  retry_count   INTEGER NOT NULL DEFAULT 0, retry_at INTEGER,
  referrer      TEXT,
  created_at    INTEGER NOT NULL, started_at INTEGER, finished_at INTEGER, removed_at INTEGER
);
CREATE INDEX jobs_state ON jobs(state);
CREATE INDEX jobs_finished ON jobs(finished_at);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE job_events (
  job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  at INTEGER NOT NULL, kind TEXT NOT NULL, detail TEXT
);
CREATE INDEX job_events_job ON job_events(job_id);
