package exporter

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lib/pq"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/postgres"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
	"gopkg.in/yaml.v3"
)

type Exporter struct {
	instanceCfg domain.InstanceConfig
	totalTables int
	progress    *ui.Progress
}

func New(cfg domain.InstanceConfig, totalTables int) *Exporter {
	return &Exporter{
		instanceCfg: cfg,
		totalTables: totalTables,
	}
}

func (e *Exporter) Export(databases []domain.DatabaseSource, sourceHost, outputFile string) (*domain.BackupData, error) {
	backup := &domain.BackupData{
		Version:    "1.0",
		ExportedAt: time.Now(),
		SourceHost: sourceHost,
		Databases:  make([]*domain.DatabaseBackup, 0, len(databases)),
	}

	// Initialize progress bar
	e.progress = ui.NewProgress(e.totalTables, "Exporting tables")

	for _, dbSource := range databases {
		db, err := postgres.Connect(e.instanceCfg, dbSource.Database)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to database %s: %w", dbSource.Database, err)
		}

		dbBackup := &domain.DatabaseBackup{
			Database: dbSource.Database,
			Tables:   make([]*domain.TableData, 0, len(dbSource.Tables)),
		}

		for _, tableName := range dbSource.Tables {
			exists, err := postgres.TableExists(db, tableName)
			if err != nil {
				db.Close()
				return nil, fmt.Errorf("failed to check if table %s.%s exists: %w", dbSource.Database, tableName, err)
			}
			if !exists {
				e.progress.IncrementWithLabel(tableName)
				continue
			}

			tableData, err := exportTable(db, tableName)
			if err != nil {
				db.Close()
				return nil, fmt.Errorf("failed to export table %s.%s: %w", dbSource.Database, tableName, err)
			}

			dbBackup.Tables = append(dbBackup.Tables, tableData)
			e.progress.IncrementWithLabel(tableName)
		}

		db.Close()
		backup.Databases = append(backup.Databases, dbBackup)
	}

	e.progress.FinishWithMessage("Export completed")

	if err := writeToFile(backup, outputFile); err != nil {
		return nil, fmt.Errorf("failed to write backup file: %w", err)
	}

	return backup, nil
}

func exportTable(db *sql.DB, tableName string) (*domain.TableData, error) {
	tableData := &domain.TableData{
		TableName: tableName,
		Rows:      make([]map[string]any, 0),
	}

	columns, err := postgres.GetTableColumns(db, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}
	tableData.Columns = columns

	query := fmt.Sprintf("SELECT * FROM %s", pq.QuoteIdentifier(tableName))
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query table: %w", err)
	}
	defer rows.Close()

	values := make([]any, len(columns))
	valuePtrs := make([]any, len(columns))
	for i := range values {
		valuePtrs[i] = &values[i]
	}

	for rows.Next() {
		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		rowMap := make(map[string]any)
		for i, colName := range columns {
			val := values[i]

			if b, ok := val.([]byte); ok {
				rowMap[colName] = string(b)
			} else {
				rowMap[colName] = val
			}
		}

		tableData.Rows = append(tableData.Rows, rowMap)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	tableData.RowCount = len(tableData.Rows)
	return tableData, nil
}

func writeToFile(backup *domain.BackupData, filename string) error {
	// Use atomic write pattern: write to temp file, then rename on success
	dir := filepath.Dir(filename)
	if dir == "" {
		dir = "."
	}

	tempFile, err := os.CreateTemp(dir, ".utmstack-export-*.tmp")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()

	// Clean up temp file on any error
	success := false
	defer func() {
		if !success {
			os.Remove(tempPath)
		}
	}()

	encoder := yaml.NewEncoder(tempFile)
	encoder.SetIndent(2)

	if err := encoder.Encode(backup); err != nil {
		encoder.Close()
		tempFile.Close()
		return fmt.Errorf("failed to encode YAML: %w", err)
	}

	if err := encoder.Close(); err != nil {
		tempFile.Close()
		return fmt.Errorf("failed to close YAML encoder: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename to target file
	if err := os.Rename(tempPath, filename); err != nil {
		return fmt.Errorf("failed to rename temp file to target: %w", err)
	}

	success = true
	return nil
}
