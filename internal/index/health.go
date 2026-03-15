package index

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// CheckIntegrity opens the SQLite database at dbPath, runs PRAGMA integrity_check,
// and returns an error if the result is anything other than "ok".
func CheckIntegrity(dbPath string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return fmt.Errorf("open database for integrity check: %w", err)
	}
	defer db.Close()

	var result string
	if err := db.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("integrity check query failed: %w", err)
	}

	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}

	return nil
}
