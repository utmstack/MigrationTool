package postgres

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
)

func Connect(cfg domain.InstanceConfig, database string) (*sql.DB, error) {
	connStr := cfg.ConnectionString(database)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	return db, nil
}

func GetTableColumns(db *sql.DB, tableName string) ([]string, error) {
	query := `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'public'
		AND table_name = $1
		ORDER BY ordinal_position
	`

	rows, err := db.Query(query, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to query columns: %w", err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var colName string
		if err := rows.Scan(&colName); err != nil {
			return nil, fmt.Errorf("failed to scan column name: %w", err)
		}
		columns = append(columns, colName)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating columns: %w", err)
	}

	if len(columns) == 0 {
		return nil, fmt.Errorf("table %s not found or has no columns", tableName)
	}

	return columns, nil
}

func TableExists(db *sql.DB, tableName string) (bool, error) {
	query := `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_schema = 'public'
			AND table_name = $1
		)
	`

	var exists bool
	err := db.QueryRow(query, tableName).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check table existence: %w", err)
	}

	return exists, nil
}

// ResetTableSequences resets all sequences for a table to MAX(column) + 1
// This is necessary after importing data with explicit IDs to avoid
// "duplicate key" errors when inserting new rows
func ResetTableSequences(db *sql.DB, tableName string) error {
	// Find all columns with sequences (SERIAL, BIGSERIAL, or IDENTITY)
	query := `
		SELECT 
			a.attname as column_name,
			pg_get_serial_sequence($1, a.attname) as sequence_name
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relname = $1
			AND n.nspname = 'public'
			AND a.attnum > 0
			AND NOT a.attisdropped
			AND pg_get_serial_sequence($1, a.attname) IS NOT NULL
	`

	rows, err := db.Query(query, tableName)
	if err != nil {
		return fmt.Errorf("failed to query sequences: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var columnName, sequenceName string
		if err := rows.Scan(&columnName, &sequenceName); err != nil {
			return fmt.Errorf("failed to scan sequence info: %w", err)
		}

		// Reset sequence to MAX(column) + 1, or 1 if table is empty
		resetQuery := fmt.Sprintf(`
			SELECT setval(%s, COALESCE((SELECT MAX(%s) FROM %s), 0) + 1, false)
		`, pq.QuoteLiteral(sequenceName), pq.QuoteIdentifier(columnName), pq.QuoteIdentifier(tableName))

		if _, err := db.Exec(resetQuery); err != nil {
			return fmt.Errorf("failed to reset sequence %s: %w", sequenceName, err)
		}
	}

	return rows.Err()
}
