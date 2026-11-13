package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"modernc.org/sqlite"

	"syllabuddy/internal/models"
)

var (
	// ErrNotFound indicates the requested record does not exist or is not owned by the caller.
	ErrNotFound = errors.New("record not found")
	// ErrConflict indicates a uniqueness constraint violation (e.g., duplicate username or email).
	ErrConflict = errors.New("record already exists")
	// ErrEventNotFound indicates the syllabus exists for the user, but the target event title was not found.
	ErrEventNotFound = errors.New("syllabus event not found")
)

// DB wraps a *sql.DB backed by pure-Go SQLite tuned for low-memory environments.
type DB struct {
	sqlDB *sql.DB
}

// Connect opens a SQLite database at dbPath (creating parent directories if needed)
// and configures WAL mode, foreign keys, busy timeout, and a 4 MB page cache.
func Connect(ctx context.Context, dbPath string) (*DB, error) {
	path := strings.TrimSpace(dbPath)
	if path == "" {
		path = "./data/syllabuddy.db"
	}

	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		dir := filepath.Dir(path)
		if dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return nil, fmt.Errorf("create sqlite data dir: %w", err)
			}
		}
	}

	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	// Single writer connection prevents SQLITE_BUSY under concurrent Go requests
	// while keeping memory overhead minimal on GCP e2-micro.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping sqlite database: %w", err)
	}

	const pragmas = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = NORMAL;
PRAGMA cache_size = -4000;
`
	if _, err := sqlDB.ExecContext(pingCtx, pragmas); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("configure sqlite pragmas: %w", err)
	}

	return &DB{sqlDB: sqlDB}, nil
}

// Close closes the underlying SQLite database handle.
func (d *DB) Close() {
	if d != nil && d.sqlDB != nil {
		_ = d.sqlDB.Close()
	}
}

// Ping verifies database connectivity.
func (d *DB) Ping(ctx context.Context) error {
	return d.sqlDB.PingContext(ctx)
}

// SQLDB exposes the underlying *sql.DB for diagnostics or custom queries.
func (d *DB) SQLDB() *sql.DB {
	return d.sqlDB
}

// Migrate creates required tables and indexes if they do not exist.
func (d *DB) Migrate(ctx context.Context) error {
	const ddl = `
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    email TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    google_id TEXT,
    created_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_google_id
    ON users(google_id)
    WHERE google_id IS NOT NULL AND google_id <> '';

CREATE TABLE IF NOT EXISTS syllabuses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    course_name TEXT NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    upload_date TEXT NOT NULL,
    file_path TEXT NOT NULL,
    parser_output TEXT
);

CREATE INDEX IF NOT EXISTS idx_syllabuses_user_id ON syllabuses(user_id);
`
	if _, err := d.sqlDB.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}

// --- Users ---

// CreateUser inserts a new local user account.
func (d *DB) CreateUser(ctx context.Context, username, email, passwordHash string) (*models.User, error) {
	now := nowTimestamp()
	const q = `
INSERT INTO users (username, email, password_hash, created_at)
VALUES (?, ?, ?, ?)`

	res, err := d.sqlDB.ExecContext(ctx, q, strings.TrimSpace(username), strings.ToLower(strings.TrimSpace(email)), passwordHash, now)
	if err != nil {
		return nil, mapSQLiteError(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("last insert id: %w", err)
	}
	return d.GetUserByID(ctx, int(id))
}

// CreateOrLinkGoogleUser finds an existing user by google_id or email, or creates a new Google-linked account.
func (d *DB) CreateOrLinkGoogleUser(ctx context.Context, preferredUsername, email, googleID string) (*models.User, error) {
	normEmail := strings.ToLower(strings.TrimSpace(email))
	if normEmail == "" || strings.TrimSpace(googleID) == "" {
		return nil, errors.New("email and googleID are required")
	}

	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin google user tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var u models.User
	var createdStr string
	err = tx.QueryRowContext(ctx, `
SELECT id, username, email, password_hash, COALESCE(google_id, ''), created_at
FROM users
WHERE google_id = ? OR LOWER(email) = ?
LIMIT 1`, googleID, normEmail).
		Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GoogleID, &createdStr)

	if err == nil {
		u.CreatedAt = parseTimestamp(createdStr)
		if u.GoogleID == "" {
			if _, updErr := tx.ExecContext(ctx, `UPDATE users SET google_id = ? WHERE id = ?`, googleID, u.ID); updErr != nil {
				return nil, mapSQLiteError(updErr)
			}
			u.GoogleID = googleID
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, fmt.Errorf("commit google user tx: %w", commitErr)
		}
		return &u, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("lookup google user: %w", err)
	}

	base := sanitizeUsername(preferredUsername, normEmail)
	username := base
	now := nowTimestamp()
	for suffix := 0; suffix < 20; suffix++ {
		if suffix > 0 {
			username = fmt.Sprintf("%s_%d", truncateStr(base, 42), suffix)
		}
		const googleOnlySentinel = "!google-oauth-only!"
		res, insertErr := tx.ExecContext(ctx, `
INSERT INTO users (username, email, password_hash, google_id, created_at)
VALUES (?, ?, ?, ?, ?)`,
			username, normEmail, googleOnlySentinel, googleID, now)
		if insertErr == nil {
			id, idErr := res.LastInsertId()
			if idErr != nil {
				return nil, fmt.Errorf("google user last insert id: %w", idErr)
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return nil, fmt.Errorf("commit new google user tx: %w", commitErr)
			}
			return &models.User{
				ID:           int(id),
				Username:     username,
				Email:        normEmail,
				PasswordHash: googleOnlySentinel,
				GoogleID:     googleID,
				CreatedAt:    parseTimestamp(now),
			}, nil
		}
		if !isUniqueViolation(insertErr) {
			return nil, mapSQLiteError(insertErr)
		}
	}

	return nil, ErrConflict
}

// GetUserByID fetches a user by primary key.
func (d *DB) GetUserByID(ctx context.Context, id int) (*models.User, error) {
	return d.scanUser(ctx, `
SELECT id, username, email, password_hash, COALESCE(google_id, ''), created_at
FROM users WHERE id = ?`, id)
}

// GetUserByEmail fetches a user by email (case-insensitive).
func (d *DB) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	return d.scanUser(ctx, `
SELECT id, username, email, password_hash, COALESCE(google_id, ''), created_at
FROM users WHERE LOWER(email) = LOWER(?)`, strings.TrimSpace(email))
}

// GetUserByUsername fetches a user by exact username.
func (d *DB) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	return d.scanUser(ctx, `
SELECT id, username, email, password_hash, COALESCE(google_id, ''), created_at
FROM users WHERE username = ?`, strings.TrimSpace(username))
}

func (d *DB) scanUser(ctx context.Context, query string, arg any) (*models.User, error) {
	var u models.User
	var createdStr string
	err := d.sqlDB.QueryRowContext(ctx, query, arg).
		Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash, &u.GoogleID, &createdStr)
	if err != nil {
		return nil, mapSQLiteError(err)
	}
	u.CreatedAt = parseTimestamp(createdStr)
	return &u, nil
}

// UpdateUsername updates the username for a given user ID.
func (d *DB) UpdateUsername(ctx context.Context, userID int, username string) (*models.User, error) {
	res, err := d.sqlDB.ExecContext(ctx, `UPDATE users SET username = ? WHERE id = ?`, strings.TrimSpace(username), userID)
	if err != nil {
		return nil, mapSQLiteError(err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, ErrNotFound
	}
	return d.GetUserByID(ctx, userID)
}

// UpdatePasswordHash replaces the bcrypt password hash for a given user ID.
func (d *DB) UpdatePasswordHash(ctx context.Context, userID int, passwordHash string) error {
	res, err := d.sqlDB.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, passwordHash, userID)
	if err != nil {
		return mapSQLiteError(err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser removes a user and cascades deletion of their syllabuses and planned courses.
func (d *DB) DeleteUser(ctx context.Context, userID int) error {
	res, err := d.sqlDB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return mapSQLiteError(err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Syllabi (strict user_id ownership on every query/update/delete) ---

// CreateSyllabus inserts a new syllabus owned by userID.
func (d *DB) CreateSyllabus(ctx context.Context, userID int, courseName, filePath string, parserOutput *models.ParserOutput) (*models.Syllabus, error) {
	var rawJSON sql.NullString
	if parserOutput != nil {
		b, err := json.Marshal(parserOutput)
		if err != nil {
			return nil, fmt.Errorf("marshal parser output: %w", err)
		}
		rawJSON = sql.NullString{String: string(b), Valid: true}
	}

	now := nowTimestamp()
	res, err := d.sqlDB.ExecContext(ctx, `
INSERT INTO syllabuses (course_name, user_id, upload_date, file_path, parser_output)
VALUES (?, ?, ?, ?, ?)`,
		truncateStr(strings.TrimSpace(courseName), 100), userID, now, truncateStr(filePath, 255), rawJSON)
	if err != nil {
		return nil, mapSQLiteError(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("syllabus last insert id: %w", err)
	}
	return d.GetSyllabusByID(ctx, int(id), userID)
}

// ListSyllabiByUser returns all syllabi owned by userID ordered newest first.
func (d *DB) ListSyllabiByUser(ctx context.Context, userID int) ([]models.Syllabus, error) {
	const q = `
SELECT id, course_name, user_id, upload_date, file_path, parser_output
FROM syllabuses
WHERE user_id = ?
ORDER BY upload_date DESC, id DESC`

	rows, err := d.sqlDB.QueryContext(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list syllabi: %w", err)
	}
	defer func() { _ = rows.Close() }()

	syllabi := make([]models.Syllabus, 0)
	for rows.Next() {
		var s models.Syllabus
		var uploadStr string
		var rawOut sql.NullString
		if err := rows.Scan(&s.ID, &s.CourseName, &s.UserID, &uploadStr, &s.FilePath, &rawOut); err != nil {
			return nil, fmt.Errorf("scan syllabus: %w", err)
		}
		s.UploadDate = parseTimestamp(uploadStr)
		if err := unmarshalParserOutput(rawOut, &s.ParserOutput); err != nil {
			return nil, err
		}
		syllabi = append(syllabi, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate syllabi: %w", err)
	}
	return syllabi, nil
}

// GetSyllabusByID returns a single syllabus only if it belongs to userID.
func (d *DB) GetSyllabusByID(ctx context.Context, syllabusID, userID int) (*models.Syllabus, error) {
	const q = `
SELECT id, course_name, user_id, upload_date, file_path, parser_output
FROM syllabuses
WHERE id = ? AND user_id = ?`

	var s models.Syllabus
	var uploadStr string
	var rawOut sql.NullString
	err := d.sqlDB.QueryRowContext(ctx, q, syllabusID, userID).
		Scan(&s.ID, &s.CourseName, &s.UserID, &uploadStr, &s.FilePath, &rawOut)
	if err != nil {
		return nil, mapSQLiteError(err)
	}
	s.UploadDate = parseTimestamp(uploadStr)
	if err := unmarshalParserOutput(rawOut, &s.ParserOutput); err != nil {
		return nil, err
	}
	return &s, nil
}

// UpdateSyllabusParserOutput updates course_name and parser_output for a syllabus owned by userID.
func (d *DB) UpdateSyllabusParserOutput(ctx context.Context, syllabusID, userID int, courseName string, parserOutput *models.ParserOutput) error {
	var rawJSON sql.NullString
	if parserOutput != nil {
		b, err := json.Marshal(parserOutput)
		if err != nil {
			return fmt.Errorf("marshal parser output: %w", err)
		}
		rawJSON = sql.NullString{String: string(b), Valid: true}
	}

	res, err := d.sqlDB.ExecContext(ctx, `
UPDATE syllabuses
SET course_name = COALESCE(NULLIF(?, ''), course_name),
    parser_output = ?
WHERE id = ? AND user_id = ?`,
		truncateStr(strings.TrimSpace(courseName), 100), rawJSON, syllabusID, userID)
	if err != nil {
		return mapSQLiteError(err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateSyllabusEventDate updates the date of a matching event title in a syllabus owned by userID.
func (d *DB) UpdateSyllabusEventDate(ctx context.Context, syllabusID, userID int, eventTitle, newDate string) (*models.Syllabus, error) {
	tx, err := d.sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin update event tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var s models.Syllabus
	var uploadStr string
	var rawOut sql.NullString
	err = tx.QueryRowContext(ctx, `
SELECT id, course_name, user_id, upload_date, file_path, parser_output
FROM syllabuses
WHERE id = ? AND user_id = ?`, syllabusID, userID).
		Scan(&s.ID, &s.CourseName, &s.UserID, &uploadStr, &s.FilePath, &rawOut)
	if err != nil {
		return nil, mapSQLiteError(err)
	}
	s.UploadDate = parseTimestamp(uploadStr)
	if err := unmarshalParserOutput(rawOut, &s.ParserOutput); err != nil {
		return nil, err
	}
	if s.ParserOutput == nil || len(s.ParserOutput.Events) == 0 {
		return nil, ErrEventNotFound
	}

	target := strings.TrimSpace(eventTitle)
	matched := false
	for i := range s.ParserOutput.Events {
		if strings.TrimSpace(s.ParserOutput.Events[i].Title) == target {
			s.ParserOutput.Events[i].Date = strings.TrimSpace(newDate)
			matched = true
		}
	}
	if !matched {
		return nil, ErrEventNotFound
	}

	updatedBytes, err := json.Marshal(s.ParserOutput)
	if err != nil {
		return nil, fmt.Errorf("marshal updated parser output: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
UPDATE syllabuses
SET parser_output = ?
WHERE id = ? AND user_id = ?`, string(updatedBytes), syllabusID, userID); err != nil {
		return nil, mapSQLiteError(err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit update event tx: %w", err)
	}
	return &s, nil
}

// DeleteSyllabus removes a syllabus only if owned by userID, returning the deleted row for file cleanup.
func (d *DB) DeleteSyllabus(ctx context.Context, syllabusID, userID int) (*models.Syllabus, error) {
	s, err := d.GetSyllabusByID(ctx, syllabusID, userID)
	if err != nil {
		return nil, err
	}
	res, err := d.sqlDB.ExecContext(ctx, `DELETE FROM syllabuses WHERE id = ? AND user_id = ?`, syllabusID, userID)
	if err != nil {
		return nil, mapSQLiteError(err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return nil, ErrNotFound
	}
	return s, nil
}

// --- Helpers ---

func nowTimestamp() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func parseTimestamp(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func mapSQLiteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

func isUniqueViolation(err error) bool {
	var sqlErr *sqlite.Error
	if errors.As(err, &sqlErr) {
		code := sqlErr.Code()
		// SQLITE_CONSTRAINT_UNIQUE (2067) or SQLITE_CONSTRAINT_PRIMARYKEY (1555)
		if code == 2067 || code == 1555 {
			return true
		}
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func unmarshalParserOutput(raw sql.NullString, dst **models.ParserOutput) error {
	if !raw.Valid {
		*dst = nil
		return nil
	}
	trimmed := strings.TrimSpace(raw.String)
	if trimmed == "" || trimmed == "null" {
		*dst = nil
		return nil
	}
	var out models.ParserOutput
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return fmt.Errorf("unmarshal parser_output: %w", err)
	}
	if out.Events == nil {
		out.Events = []models.SyllabusEvent{}
	}
	*dst = &out
	return nil
}

func sanitizeUsername(preferred, email string) string {
	raw := strings.TrimSpace(preferred)
	if raw == "" {
		if at := strings.IndexByte(email, '@'); at > 0 {
			raw = email[:at]
		} else {
			raw = "student"
		}
	}
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	res := b.String()
	if res == "" {
		res = "student"
	}
	return truncateStr(res, 42)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
