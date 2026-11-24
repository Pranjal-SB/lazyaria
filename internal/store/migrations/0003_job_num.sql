ALTER TABLE jobs ADD COLUMN num INTEGER NOT NULL DEFAULT 0;
UPDATE jobs SET num = (SELECT r.n FROM (SELECT id, row_number() OVER (ORDER BY id) AS n FROM jobs) r WHERE r.id = jobs.id);
INSERT INTO meta(key, value) SELECT 'last_job_num', CAST(coalesce(max(num), 0) AS TEXT) FROM jobs;
CREATE UNIQUE INDEX jobs_num ON jobs(num);
