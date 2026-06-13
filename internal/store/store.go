// Package store persists seen jobs in SQLite using the pure-Go driver
// (modernc.org/sqlite), so the binary compiles with CGO_ENABLED=0 and runs in a
// scratch/distroless container. job_id is the primary key, which gives dedup
// for free.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver

	"hireme/internal/jsearch"
)

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
    job_id         TEXT PRIMARY KEY,
    title          TEXT NOT NULL,
    employer       TEXT,
    location       TEXT,
    publisher      TEXT,
    apply_link     TEXT,
    remote         INTEGER NOT NULL DEFAULT 0,
    posted_at_unix INTEGER,
    seen_at        INTEGER NOT NULL,
    notified       INTEGER NOT NULL DEFAULT 0,
    notified_at    INTEGER
);
CREATE INDEX IF NOT EXISTS idx_jobs_notified ON jobs(notified);
`

// Open opens (and migrates) the SQLite database at path. Pragmas are passed via
// the DSN: WAL for non-blocking reads, a busy timeout to retry on locks, and
// synchronous=NORMAL which is crash-safe under WAL.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)",
		path,
	)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite is single-writer; one connection avoids needless lock contention.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// SaveNew inserts a job if its job_id has never been seen. It returns true when
// the row was newly inserted (i.e. this is a genuinely new job), false when it
// already existed. The insert is atomic, so this also serves as the dedup gate.
func (s *Store) SaveNew(ctx context.Context, j jsearch.Job) (isNew bool, err error) {
	res, err := s.db.ExecContext(ctx, `
        INSERT INTO jobs (job_id, title, employer, location, publisher, apply_link, remote, posted_at_unix, seen_at, notified)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)
        ON CONFLICT(job_id) DO NOTHING`,
		j.JobID, j.Title, j.Employer, j.Location, j.Publisher, j.ApplyLink,
		boolToInt(j.Remote), j.PostedAtUnix, time.Now().Unix(),
	)
	if err != nil {
		return false, fmt.Errorf("save job %s: %w", j.JobID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("rows affected: %w", err)
	}
	return n == 1, nil
}

// MarkNotified records that the user has been pushed this job.
func (s *Store) MarkNotified(ctx context.Context, jobID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET notified = 1, notified_at = ? WHERE job_id = ?`,
		time.Now().Unix(), jobID,
	)
	if err != nil {
		return fmt.Errorf("mark notified %s: %w", jobID, err)
	}
	return nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
