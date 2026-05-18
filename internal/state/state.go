package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS seen (
  source       TEXT NOT NULL,
  msg_id       TEXT NOT NULL,
  first_seen   TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  section      TEXT,
  clean_title  TEXT,
  body         TEXT,
  posted_at    TIMESTAMP,
  event_time   TIMESTAMP,
  attachments  TEXT,
  PRIMARY KEY (source, msg_id)
);

CREATE INDEX IF NOT EXISTS seen_event_time ON seen(event_time) WHERE event_time IS NOT NULL;
CREATE INDEX IF NOT EXISTS seen_first_seen ON seen(first_seen);

CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

type Item struct {
	Source      string
	MsgID       string
	Section     string
	CleanTitle  string
	Body        string
	PostedAt    *time.Time
	EventTime   *time.Time
	Attachments []string
}

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := ensureDir(dir); err != nil {
			return nil, fmt.Errorf("ensure sqlite dir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// MarkSeen inserts the item if not present. Returns true if the row was newly
// inserted (genuinely new), false if it already existed.
func (s *Store) MarkSeen(ctx context.Context, it Item) (bool, error) {
	attachJSON, err := encodeAttachments(it.Attachments)
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO seen
			(source, msg_id, section, clean_title, body, posted_at, event_time, attachments)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		it.Source, it.MsgID, it.Section, it.CleanTitle, it.Body,
		nullableTime(it.PostedAt), nullableTime(it.EventTime), attachJSON,
	)
	if err != nil {
		return false, fmt.Errorf("insert seen: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *Store) IsBootstrapped(ctx context.Context) (bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'bootstrapped_at'`).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return v != "", nil
}

func (s *Store) SetBootstrapped(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO meta(key, value) VALUES ('bootstrapped_at', ?)`,
		time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

// RecentSeen returns the most recent N rows ordered by first_seen desc. Useful
// for the debug endpoint.
func (s *Store) RecentSeen(ctx context.Context, limit int) ([]Item, error) {
	return s.queryItems(ctx, `
		SELECT source, msg_id, section, clean_title, body, posted_at, event_time, attachments
		FROM seen
		ORDER BY first_seen DESC
		LIMIT ?
	`, limit)
}

// CalendarItems returns all seen items that have a usable timestamp
// (event_time if set, else posted_at), ordered by that timestamp ascending.
// Items with neither are skipped.
func (s *Store) CalendarItems(ctx context.Context) ([]Item, error) {
	return s.queryItems(ctx, `
		SELECT source, msg_id, section, clean_title, body, posted_at, event_time, attachments
		FROM seen
		WHERE event_time IS NOT NULL OR posted_at IS NOT NULL
		ORDER BY COALESCE(event_time, posted_at) ASC
	`)
}

// ItemsSince returns every seen item whose first_seen (or posted_at, if set)
// is at or after `since`, ordered by posted_at desc. Used by the dashboard
// handler.
func (s *Store) ItemsSince(ctx context.Context, since time.Time) ([]Item, error) {
	return s.queryItems(ctx, `
		SELECT source, msg_id, section, clean_title, body, posted_at, event_time, attachments
		FROM seen
		WHERE COALESCE(posted_at, first_seen) >= ?
		ORDER BY COALESCE(posted_at, first_seen) DESC
	`, since.UTC())
}

func (s *Store) queryItems(ctx context.Context, q string, args ...any) ([]Item, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Item
	for rows.Next() {
		var (
			it          Item
			section     sql.NullString
			cleanTitle  sql.NullString
			body        sql.NullString
			postedAt    sql.NullTime
			eventTime   sql.NullTime
			attachments sql.NullString
		)
		if err := rows.Scan(&it.Source, &it.MsgID, &section, &cleanTitle, &body, &postedAt, &eventTime, &attachments); err != nil {
			return nil, err
		}
		it.Section = section.String
		it.CleanTitle = cleanTitle.String
		it.Body = body.String
		if postedAt.Valid {
			t := postedAt.Time
			it.PostedAt = &t
		}
		if eventTime.Valid {
			t := eventTime.Time
			it.EventTime = &t
		}
		if attachments.Valid && attachments.String != "" {
			_ = json.Unmarshal([]byte(attachments.String), &it.Attachments)
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func encodeAttachments(a []string) (string, error) {
	if len(a) == 0 {
		return "", nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
