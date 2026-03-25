package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"

	"interest-digest/internal/digest"
)

type Store struct {
	db *sqlx.DB
}

func Open(path string) (*Store, error) {
	db, err := sqlx.Open("sqlite3", path+"?_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	if err := s.migrateFromLegacyIfNeeded(); err != nil {
		return err
	}
	if _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS jobs (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  source_text TEXT NOT NULL,
  preferences_override TEXT,
  speaker_split TEXT NOT NULL DEFAULT 'auto',
  result_json TEXT,
  error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`); err != nil {
		return err
	}
	if err := s.migrateJobsSpeakerSplit(); err != nil {
		return err
	}
	if err := s.migrateJobsPreferences(); err != nil {
		return err
	}
	return s.migrateJobsProgress()
}

func (s *Store) migrateFromLegacyIfNeeded() error {
	var hasJobs int
	if err := s.db.Get(&hasJobs, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='jobs'`); err != nil {
		return err
	}
	if hasJobs == 0 {
		return nil
	}
	var n int
	if err := s.db.Get(&n, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='profiles'`); err != nil {
		return err
	}
	var hasProfileID int
	_ = s.db.Get(&hasProfileID, `SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name='profile_id'`)
	if n == 0 && hasProfileID == 0 {
		return nil
	}
	return s.rebuildJobsWithoutLegacy(n > 0)
}

func combineLegacyPrefs(avoid, lean string) string {
	switch {
	case avoid == "" && lean == "":
		return ""
	case avoid == "":
		return "Keep or prioritize content about:\n" + lean
	case lean == "":
		return "Skip or omit content about:\n" + avoid
	default:
		return "Keep or prioritize content about:\n" + lean + "\n\nSkip or omit content about:\n" + avoid
	}
}

func (s *Store) rebuildJobsWithoutLegacy(loadProfiles bool) error {
	tx, err := s.db.Beginx()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
CREATE TABLE jobs_new (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL,
  source_text TEXT NOT NULL,
  preferences_override TEXT,
  speaker_split TEXT NOT NULL DEFAULT 'auto',
  progress_phase TEXT NOT NULL DEFAULT '',
  progress_step INTEGER NOT NULL DEFAULT 0,
  progress_total INTEGER NOT NULL DEFAULT 0,
  result_json TEXT,
  error TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
`); err != nil {
		return err
	}

	rows, err := tx.Queryx(`
SELECT id, status, source_text, profile_id, preferences_override, avoid_override, lean_override,
       speaker_split, progress_phase, progress_step, progress_total, result_json, error, created_at, updated_at
FROM jobs`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var j struct {
			ID                  string         `db:"id"`
			Status              string         `db:"status"`
			SourceText          string         `db:"source_text"`
			ProfileID           sql.NullString `db:"profile_id"`
			PreferencesOverride sql.NullString `db:"preferences_override"`
			AvoidOverride       sql.NullString `db:"avoid_override"`
			LeanOverride        sql.NullString `db:"lean_override"`
			SpeakerSplit        string         `db:"speaker_split"`
			ProgressPhase       string         `db:"progress_phase"`
			ProgressStep        int            `db:"progress_step"`
			ProgressTotal       int            `db:"progress_total"`
			ResultJSON          sql.NullString `db:"result_json"`
			Error               sql.NullString `db:"error"`
			CreatedAt           string         `db:"created_at"`
			UpdatedAt           string         `db:"updated_at"`
		}
		if err := rows.StructScan(&j); err != nil {
			return err
		}
		prefs := resolvePrefsForMigration(tx, loadProfiles, j.PreferencesOverride, j.AvoidOverride, j.LeanOverride, j.ProfileID)
		ss := strings.TrimSpace(j.SpeakerSplit)
		if ss == "" {
			ss = "auto"
		}
		var res, errMsg interface{}
		if j.ResultJSON.Valid {
			res = j.ResultJSON.String
		}
		if j.Error.Valid {
			errMsg = j.Error.String
		}
		_, err = tx.Exec(`
INSERT INTO jobs_new (id, status, source_text, preferences_override, speaker_split, progress_phase, progress_step, progress_total, result_json, error, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
			j.ID, j.Status, j.SourceText, nullIfEmpty(prefs), ss, j.ProgressPhase, j.ProgressStep, j.ProgressTotal, res, errMsg, j.CreatedAt, j.UpdatedAt,
		)
		if err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	if _, err := tx.Exec(`DROP TABLE jobs`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE jobs_new RENAME TO jobs`); err != nil {
		return err
	}
	if loadProfiles {
		if _, err := tx.Exec(`DROP TABLE IF EXISTS profiles`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func nullIfEmpty(s string) interface{} {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func resolvePrefsForMigration(tx *sqlx.Tx, loadProfiles bool, pref, avoid, lean sql.NullString, profileID sql.NullString) string {
	if pref.Valid && strings.TrimSpace(pref.String) != "" {
		return strings.TrimSpace(pref.String)
	}
	var a, l string
	if avoid.Valid {
		a = strings.TrimSpace(avoid.String)
	}
	if lean.Valid {
		l = strings.TrimSpace(lean.String)
	}
	if loadProfiles && profileID.Valid && strings.TrimSpace(profileID.String) != "" {
		var p struct {
			Avoid string `db:"avoid_text"`
			Lean  string `db:"lean_text"`
		}
		_ = tx.Get(&p, `SELECT avoid_text, lean_text FROM profiles WHERE id=?`, profileID.String)
		if a == "" {
			a = strings.TrimSpace(p.Avoid)
		}
		if l == "" {
			l = strings.TrimSpace(p.Lean)
		}
	}
	return combineLegacyPrefs(a, l)
}

func (s *Store) migrateJobsSpeakerSplit() error {
	var n int
	if err := s.db.Get(&n, `SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name = 'speaker_split'`); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(`ALTER TABLE jobs ADD COLUMN speaker_split TEXT NOT NULL DEFAULT 'auto'`)
	return err
}

func (s *Store) migrateJobsPreferences() error {
	var n int
	if err := s.db.Get(&n, `SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name = 'preferences_override'`); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(`ALTER TABLE jobs ADD COLUMN preferences_override TEXT`)
	return err
}

func (s *Store) migrateJobsProgress() error {
	for _, col := range []struct {
		name string
		ddl  string
	}{
		{"progress_phase", `ALTER TABLE jobs ADD COLUMN progress_phase TEXT NOT NULL DEFAULT ''`},
		{"progress_step", `ALTER TABLE jobs ADD COLUMN progress_step INTEGER NOT NULL DEFAULT 0`},
		{"progress_total", `ALTER TABLE jobs ADD COLUMN progress_total INTEGER NOT NULL DEFAULT 0`},
	} {
		var n int
		if err := s.db.Get(&n, `SELECT COUNT(*) FROM pragma_table_info('jobs') WHERE name=?`, col.name); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		if _, err := s.db.Exec(col.ddl); err != nil {
			return err
		}
	}
	return nil
}

type JobRow struct {
	ID                  string         `db:"id"`
	Status              string         `db:"status"`
	SourceText          string         `db:"source_text"`
	PreferencesOverride sql.NullString `db:"preferences_override"`
	SpeakerSplit        string         `db:"speaker_split"`
	ProgressPhase       string         `db:"progress_phase"`
	ProgressStep        int            `db:"progress_step"`
	ProgressTotal       int            `db:"progress_total"`
	ResultJSON          sql.NullString `db:"result_json"`
	Error               sql.NullString `db:"error"`
	CreatedAt           string         `db:"created_at"`
	UpdatedAt           string         `db:"updated_at"`
}

func (s *Store) CreateJob(ctx context.Context, source string, preferencesOverride *string, speakerSplit string) (string, error) {
	id := uuid.NewString()
	now := time.Now().UTC().Format(time.RFC3339)
	var po interface{}
	if preferencesOverride != nil {
		po = *preferencesOverride
	}
	if speakerSplit == "" {
		speakerSplit = "auto"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (id, status, source_text, preferences_override, speaker_split, created_at, updated_at)
		 VALUES (?,?,?,?,?,?,?)`,
		id, "pending", source, po, speakerSplit, now, now,
	)
	return id, err
}

func (s *Store) SetJobRunning(ctx context.Context, id string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status='running', updated_at=? WHERE id=?`, now, id)
	return err
}

// SetJobProgress updates coarse pipeline progress for polling (e.g. LLM batch i of n).
func (s *Store) SetJobProgress(ctx context.Context, id, phase string, step, total int) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET progress_phase=?, progress_step=?, progress_total=?, updated_at=? WHERE id=?`,
		phase, step, total, now, id,
	)
	return err
}

func (s *Store) SetJobResult(ctx context.Context, id string, doc *digest.DigestDocument) error {
	now := time.Now().UTC().Format(time.RFC3339)
	b, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`UPDATE jobs SET status='completed', result_json=?, error=NULL, progress_phase='', progress_step=0, progress_total=0, updated_at=? WHERE id=?`,
		string(b), now, id,
	)
	return err
}

func (s *Store) SetJobFailed(ctx context.Context, id, msg string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status='failed', error=?, progress_phase='', progress_step=0, progress_total=0, updated_at=? WHERE id=?`,
		msg, now, id,
	)
	return err
}

func (s *Store) GetJob(ctx context.Context, id string) (*JobRow, error) {
	var j JobRow
	err := s.db.GetContext(ctx, &j, `
		SELECT id, status, source_text, preferences_override, speaker_split,
		       progress_phase, progress_step, progress_total, result_json, error, created_at, updated_at
		FROM jobs
		WHERE id=?`,
		id,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

func (s *Store) DigestFromJob(j *JobRow) (*digest.DigestDocument, error) {
	if j.Status == "completed" && j.ResultJSON.Valid && j.ResultJSON.String != "" {
		var d digest.DigestDocument
		if err := json.Unmarshal([]byte(j.ResultJSON.String), &d); err != nil {
			return nil, err
		}
		return &d, nil
	}
	d := &digest.DigestDocument{
		JobID:           j.ID,
		Status:          j.Status,
		SourceText:      j.SourceText,
		Spans:           []digest.Span{},
		OmittedRanges:   []digest.OmittedRange{},
		ProgressPhase:   j.ProgressPhase,
		ProgressStep:    j.ProgressStep,
		ProgressTotal:   j.ProgressTotal,
	}
	if j.Error.Valid {
		d.Error = j.Error.String
	}
	return d, nil
}
