package scheduler

import (
	"context"
	"database/sql"
)

// SQLDeliveries is the MySQL-backed DeliveryLog. Like userstore, it creates
// its own table on startup; the database must already exist.
type SQLDeliveries struct {
	db *sql.DB
}

func NewSQLDeliveries(db *sql.DB) (*SQLDeliveries, error) {
	// period is the report's first day as YYYY-MM-DD text rather than a DATE,
	// so no driver or session time zone can shift it.
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS report_deliveries (
			chat_id VARCHAR(255) NOT NULL,
			kind VARCHAR(16) NOT NULL,
			period VARCHAR(10) NOT NULL,
			sent_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (chat_id, kind, period)
		)
	`)
	if err != nil {
		return nil, err
	}
	return &SQLDeliveries{db: db}, nil
}

func (d *SQLDeliveries) Delivered(ctx context.Context, chatID, kind, period string) (bool, error) {
	var n int
	err := d.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM report_deliveries WHERE chat_id = ? AND kind = ? AND period = ?",
		chatID, kind, period).Scan(&n)
	return n > 0, err
}

func (d *SQLDeliveries) Record(ctx context.Context, chatID, kind, period string) error {
	_, err := d.db.ExecContext(ctx,
		"INSERT IGNORE INTO report_deliveries (chat_id, kind, period) VALUES (?, ?, ?)",
		chatID, kind, period)
	return err
}
