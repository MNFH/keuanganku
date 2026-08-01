package userstore

import (
	"database/sql"
	"encoding/json"
	"os"
)

type User struct {
	SpreadsheetID string `json:"spreadsheet_id"`
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) (*Store, error) {
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			jid VARCHAR(255) PRIMARY KEY,
			spreadsheet_id VARCHAR(255) NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (s *Store) Get(jid string) (User, bool) {
	var u User
	err := s.db.QueryRow("SELECT spreadsheet_id FROM users WHERE jid = ?", jid).Scan(&u.SpreadsheetID)
	if err != nil {
		return User{}, false
	}
	return u, true
}

func (s *Store) Set(jid string, u User) error {
	_, err := s.db.Exec(
		"INSERT INTO users (jid, spreadsheet_id) VALUES (?, ?) ON DUPLICATE KEY UPDATE spreadsheet_id = VALUES(spreadsheet_id)",
		jid, u.SpreadsheetID,
	)
	return err
}

func (s *Store) Delete(jid string) error {
	_, err := s.db.Exec("DELETE FROM users WHERE jid = ?", jid)
	return err
}

// All returns every registered chat JID mapped to its User record.
func (s *Store) All() (map[string]User, error) {
	rows, err := s.db.Query("SELECT jid, spreadsheet_id FROM users")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]User{}
	for rows.Next() {
		var jid string
		var u User
		if err := rows.Scan(&jid, &u.SpreadsheetID); err != nil {
			return nil, err
		}
		result[jid] = u
	}
	return result, rows.Err()
}

func (s *Store) AnyUses(spreadsheetID string) bool {
	var exists int
	err := s.db.QueryRow("SELECT 1 FROM users WHERE spreadsheet_id = ? LIMIT 1", spreadsheetID).Scan(&exists)
	return err == nil
}

// MigrateFromJSON imports a legacy users.json file into the users table, then
// renames the file so it isn't re-imported on the next startup. No-op if the
// file doesn't exist.
func (s *Store) MigrateFromJSON(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var legacy map[string]User
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}

	for jid, u := range legacy {
		if err := s.Set(jid, u); err != nil {
			return err
		}
	}

	return os.Rename(path, path+".migrated")
}
