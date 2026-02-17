package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Feed struct {
	ID          int64
	URL         string
	Title       string
	Description string
	CreatedAt   time.Time
	LastReadAt  time.Time
	UnreadCount int
	Position    int
}

type Entry struct {
	ID          int64
	FeedID      int64
	Title       string
	Link        string
	Description string
	Content     string
	PublishedAt time.Time
	Read        bool
}

var database *sql.DB

func InitDB() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dbPath := filepath.Join(home, ".config", "lazyrss")
	if err := os.MkdirAll(dbPath, 0755); err != nil {
		return err
	}

	fullPath := filepath.Join(dbPath, "rss.db")
	// Add pragma for WAL mode and busy timeout to handle concurrent access
	db, err := sql.Open("sqlite", fullPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}

	if err := db.Ping(); err != nil {
		return err
	}

	database = db

	if err := createTables(); err != nil {
		return err
	}

	return migrate()
}

func migrate() error {
	// Migration to add last_read_at
	_, _ = database.Exec("ALTER TABLE feeds ADD COLUMN last_read_at DATETIME DEFAULT '1970-01-01 00:00:00'")
	// Migration to add position
	_, _ = database.Exec("ALTER TABLE feeds ADD COLUMN position INTEGER DEFAULT 0")

	// If all positions are 0, initialize them based on current order
	var count int
	database.QueryRow("SELECT COUNT(*) FROM feeds WHERE position != 0").Scan(&count)
	if count == 0 {
		rows, err := database.Query("SELECT id FROM feeds ORDER BY title ASC")
		if err == nil {
			defer rows.Close()
			pos := 0
			for rows.Next() {
				var id int64
				rows.Scan(&id)
				database.Exec("UPDATE feeds SET position = ? WHERE id = ?", pos, id)
				pos++
			}
		}
	}
	return nil
}

func createTables() error {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS feeds (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT UNIQUE NOT NULL,
			title TEXT,
			description TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_read_at DATETIME DEFAULT '1970-01-01 00:00:00',
			position INTEGER DEFAULT 0
		);`,
		`CREATE TABLE IF NOT EXISTS entries (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			feed_id INTEGER NOT NULL,
			title TEXT,
			link TEXT UNIQUE NOT NULL,
			description TEXT,
			content TEXT,
			published_at DATETIME,
			read BOOLEAN DEFAULT 0,
			FOREIGN KEY (feed_id) REFERENCES feeds(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_entries_feed_id ON entries(feed_id, published_at DESC);`,
	}

	for _, query := range queries {
		if _, err := database.Exec(query); err != nil {
			return err
		}
	}
	return nil
}

func GetFeeds() ([]Feed, error) {
	query := `
		SELECT f.id, f.url, f.title, f.description, f.created_at, f.last_read_at, f.position,
		       (SELECT COUNT(*) FROM entries e WHERE e.feed_id = f.id AND e.published_at > f.last_read_at) as unread_count
		FROM feeds f 
		ORDER BY f.position ASC, f.title ASC`
	rows, err := database.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var feeds []Feed
	for rows.Next() {
		var f Feed
		if err := rows.Scan(&f.ID, &f.URL, &f.Title, &f.Description, &f.CreatedAt, &f.LastReadAt, &f.Position, &f.UnreadCount); err != nil {
			return nil, err
		}
		feeds = append(feeds, f)
	}
	return feeds, nil
}

func SwapFeedPositions(idA, posA, idB, posB int) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE feeds SET position = ? WHERE id = ?", posB, idA)
	if err != nil {
		tx.Rollback()
		return err
	}
	_, err = tx.Exec("UPDATE feeds SET position = ? WHERE id = ?", posA, idB)
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func MarkFeedAsRead(id int64) error {
	_, err := database.Exec("UPDATE feeds SET last_read_at = CURRENT_TIMESTAMP WHERE id = ?", id)
	return err
}

func AddFeed(url, title, desc string) (int64, error) {
	var maxPos int
	database.QueryRow("SELECT COALESCE(MAX(position), -1) FROM feeds").Scan(&maxPos)
	res, err := database.Exec("INSERT OR IGNORE INTO feeds (url, title, description, position) VALUES (?, ?, ?, ?)", url, title, desc, maxPos+1)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func DeleteFeed(id int64) error {
	_, err := database.Exec("DELETE FROM feeds WHERE id = ?", id)
	return err
}

func SaveEntries(feedID int64, entries []Entry) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO entries (feed_id, title, link, description, content, published_at) 
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer stmt.Close()

	for _, e := range entries {
		_, err := stmt.Exec(feedID, e.Title, e.Link, e.Description, e.Content, e.PublishedAt)
		if err != nil {
			tx.Rollback()
			return err
		}
	}

	return tx.Commit()
}

func GetEntries(feedID int64) ([]Entry, error) {
	rows, err := database.Query("SELECT id, feed_id, title, link, description, content, published_at, read FROM entries WHERE feed_id = ? ORDER BY published_at DESC", feedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.FeedID, &e.Title, &e.Link, &e.Description, &e.Content, &e.PublishedAt, &e.Read); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func MarkAsRead(entryID int64) error {
	_, err := database.Exec("UPDATE entries SET read = 1 WHERE id = ?", entryID)
	return err
}

