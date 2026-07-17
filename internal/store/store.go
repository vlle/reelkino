// Package store — SQLite-хранилище списка фильмов.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const (
	StatusWant    = "want"
	StatusWatched = "watched"
)

type Movie struct {
	ID         int64   `json:"id"`
	TGUserID   int64   `json:"-"`
	Title      string  `json:"title"`
	Year       int     `json:"year"`
	Kind       string  `json:"kind"` // movie | series | ...
	PosterURL  string  `json:"poster_url"`
	RatingKP   float64 `json:"rating_kp"`
	RatingIMDb float64 `json:"rating_imdb"`
	KPID       int64   `json:"kp_id"`
	IMDbID     string  `json:"imdb_id"`
	KPURL      string  `json:"kp_url"`
	IMDbURL    string  `json:"imdb_url"`
	ReelURL    string  `json:"reel_url"`
	ReelVideo  string  `json:"reel_video"` // basename скачанного mp4 в media-каталоге
	ReelThumb  string  `json:"reel_thumb"` // basename превью
	Comment    string  `json:"comment"`
	Status     string  `json:"status"`
	CreatedAt  string  `json:"created_at"`
	WatchedAt  string  `json:"watched_at"`
}

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS movies (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	tg_user_id  INTEGER NOT NULL,
	title       TEXT NOT NULL,
	year        INTEGER NOT NULL DEFAULT 0,
	kind        TEXT NOT NULL DEFAULT 'movie',
	poster_url  TEXT NOT NULL DEFAULT '',
	rating_kp   REAL NOT NULL DEFAULT 0,
	rating_imdb REAL NOT NULL DEFAULT 0,
	kp_id       INTEGER NOT NULL DEFAULT 0,
	imdb_id     TEXT NOT NULL DEFAULT '',
	kp_url      TEXT NOT NULL DEFAULT '',
	imdb_url    TEXT NOT NULL DEFAULT '',
	reel_url    TEXT NOT NULL DEFAULT '',
	reel_video  TEXT NOT NULL DEFAULT '',
	reel_thumb  TEXT NOT NULL DEFAULT '',
	comment     TEXT NOT NULL DEFAULT '',
	status      TEXT NOT NULL DEFAULT 'want',
	created_at  TEXT NOT NULL,
	watched_at  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_movies_user ON movies(tg_user_id, status);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// single-writer: modernc.org/sqlite не любит конкурентные записи
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// migrate доливает колонки в базы, созданные старыми версиями.
func migrate(db *sql.DB) error {
	existing := map[string]bool{}
	rows, err := db.Query(`PRAGMA table_info(movies)`)
	if err != nil {
		return fmt.Errorf("store: table_info: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("store: table_info scan: %w", err)
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, col := range []string{"reel_video", "reel_thumb"} {
		if existing[col] {
			continue
		}
		if _, err := db.Exec(`ALTER TABLE movies ADD COLUMN ` + col + ` TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("store: add column %s: %w", col, err)
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// Add вставляет фильм; повторная ссылка на тот же фильм у того же
// пользователя обновляет reel_url/comment вместо дубля.
func (s *Store) Add(m *Movie) (created bool, err error) {
	if m.KPID != 0 {
		var existing int64
		err := s.db.QueryRow(
			`SELECT id FROM movies WHERE tg_user_id=? AND kp_id=?`, m.TGUserID, m.KPID,
		).Scan(&existing)
		if err == nil {
			_, err = s.db.Exec(
				`UPDATE movies SET reel_url=CASE WHEN ?<>'' THEN ? ELSE reel_url END,
				                   comment =CASE WHEN ?<>'' THEN ? ELSE comment END
				 WHERE id=?`,
				m.ReelURL, m.ReelURL, m.Comment, m.Comment, existing,
			)
			m.ID = existing
			return false, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("store: add lookup: %w", err)
		}
	}
	m.CreatedAt = now()
	m.Status = StatusWant
	res, err := s.db.Exec(
		`INSERT INTO movies (tg_user_id, title, year, kind, poster_url, rating_kp, rating_imdb,
		                     kp_id, imdb_id, kp_url, imdb_url, reel_url, comment, status, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		m.TGUserID, m.Title, m.Year, m.Kind, m.PosterURL, m.RatingKP, m.RatingIMDb,
		m.KPID, m.IMDbID, m.KPURL, m.IMDbURL, m.ReelURL, m.Comment, m.Status, m.CreatedAt,
	)
	if err != nil {
		return false, fmt.Errorf("store: insert: %w", err)
	}
	m.ID, _ = res.LastInsertId()
	return true, nil
}

// SetReelMedia сохраняет имена скачанных файлов рилса.
func (s *Store) SetReelMedia(id int64, video, thumb string) error {
	_, err := s.db.Exec(`UPDATE movies SET reel_video=?, reel_thumb=? WHERE id=?`, video, thumb, id)
	if err != nil {
		return fmt.Errorf("store: set reel media: %w", err)
	}
	return nil
}

const cols = `id, tg_user_id, title, year, kind, poster_url, rating_kp, rating_imdb,
	kp_id, imdb_id, kp_url, imdb_url, reel_url, reel_video, reel_thumb, comment, status, created_at, watched_at`

func scan(row interface{ Scan(...any) error }) (*Movie, error) {
	var m Movie
	err := row.Scan(&m.ID, &m.TGUserID, &m.Title, &m.Year, &m.Kind, &m.PosterURL,
		&m.RatingKP, &m.RatingIMDb, &m.KPID, &m.IMDbID, &m.KPURL, &m.IMDbURL,
		&m.ReelURL, &m.ReelVideo, &m.ReelThumb, &m.Comment, &m.Status, &m.CreatedAt, &m.WatchedAt)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// List возвращает фильмы пользователя, новые сверху; status "" — все.
func (s *Store) List(tgUserID int64, status string, limit int) ([]*Movie, error) {
	q := `SELECT ` + cols + ` FROM movies WHERE tg_user_id=?`
	args := []any{tgUserID}
	if status != "" {
		q += ` AND status=?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list: %w", err)
	}
	defer rows.Close()
	var out []*Movie
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) Get(tgUserID, id int64) (*Movie, error) {
	m, err := scan(s.db.QueryRow(
		`SELECT `+cols+` FROM movies WHERE id=? AND tg_user_id=?`, id, tgUserID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get: %w", err)
	}
	return m, nil
}

// Random — случайный непросмотренный фильм пользователя.
func (s *Store) Random(tgUserID int64) (*Movie, error) {
	m, err := scan(s.db.QueryRow(
		`SELECT `+cols+` FROM movies WHERE tg_user_id=? AND status=? ORDER BY RANDOM() LIMIT 1`,
		tgUserID, StatusWant))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: random: %w", err)
	}
	return m, nil
}

func (s *Store) SetStatus(tgUserID, id int64, status string) error {
	if status != StatusWant && status != StatusWatched {
		return fmt.Errorf("store: неизвестный статус %q", status)
	}
	watchedAt := ""
	if status == StatusWatched {
		watchedAt = now()
	}
	res, err := s.db.Exec(
		`UPDATE movies SET status=?, watched_at=? WHERE id=? AND tg_user_id=?`,
		status, watchedAt, id, tgUserID)
	if err != nil {
		return fmt.Errorf("store: set status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: фильм %d не найден", id)
	}
	return nil
}

func (s *Store) Delete(tgUserID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM movies WHERE id=? AND tg_user_id=?`, id, tgUserID)
	if err != nil {
		return fmt.Errorf("store: delete: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("store: фильм %d не найден", id)
	}
	return nil
}
