package importer

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/lib/pq"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/postgres"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/yaml"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
	goyaml "gopkg.in/yaml.v3"
)

type Importer struct {
	instanceCfg domain.InstanceConfig
	totalTables int
	progress    *ui.Progress
}

func New(cfg domain.InstanceConfig, totalTables int) *Importer {
	return &Importer{
		instanceCfg: cfg,
		totalTables: totalTables,
	}
}

func (i *Importer) Import(inputFile string) error {
	backup, err := readFromFile(inputFile)
	if err != nil {
		return fmt.Errorf("failed to read backup file: %w", err)
	}

	if len(backup.Databases) == 0 && len(backup.Tables) > 0 {
		backup.Databases = []*domain.DatabaseBackup{
			{
				Database: config.DefaultDatabase,
				Tables:   backup.Tables,
			},
		}
	} else if len(backup.Databases) == 0 {
		return fmt.Errorf("backup file contains no data")
	}

	if backup.InternalKey != "" {
		if err := yaml.WriteInternalKey(config.UTMStackYMLPath, backup.InternalKey); err != nil {
			return fmt.Errorf("failed to write internal key: %w", err)
		}
	}

	ui.Info("Starting data import")

	// Initialize progress bar
	i.progress = ui.NewProgress(i.totalTables, "Importing tables")

	for _, dbBackup := range backup.Databases {
		db, err := postgres.Connect(i.instanceCfg, dbBackup.Database)
		if err != nil {
			return fmt.Errorf("failed to connect to database %s: %w", dbBackup.Database, err)
		}

		for _, tableData := range dbBackup.Tables {

			exists, err := postgres.TableExists(db, tableData.TableName)
			if err != nil {
				db.Close()
				return fmt.Errorf("failed to check if table exists: %w", err)
			}
			if !exists {
				i.progress.IncrementWithLabel(tableData.TableName)
				continue // Skip tables that don't exist in destination
			}

			if err := i.importTable(db, tableData); err != nil {
				db.Close()
				return fmt.Errorf("failed to import table %s: %w", tableData.TableName, err)
			}

			// Reset sequences after import to avoid "duplicate key" errors
			// when inserting new rows (sequences need to be updated to MAX(id) + 1)
			if err := postgres.ResetTableSequences(db, tableData.TableName); err != nil {
				db.Close()
				return fmt.Errorf("failed to reset sequences for table %s: %w", tableData.TableName, err)
			}

			i.progress.IncrementWithLabel(tableData.TableName)
		}

		db.Close()
	}

	i.progress.FinishWithMessage("Import completed")
	return nil
}

func readFromFile(filename string) (*domain.BackupData, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	var backup domain.BackupData
	if err := goyaml.Unmarshal(data, &backup); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return &backup, nil
}

func (i *Importer) importTable(db *sql.DB, tableData *domain.TableData) error {
	if len(tableData.Rows) == 0 {
		return nil
	}

	destColumns, err := postgres.GetTableColumns(db, tableData.TableName)
	if err != nil {
		return fmt.Errorf("failed to get destination table columns: %w", err)
	}

	destColumnsMap := make(map[string]bool)
	for _, col := range destColumns {
		destColumnsMap[col] = true
	}

	var validColumns []string
	for _, col := range tableData.Columns {
		if destColumnsMap[col] {
			validColumns = append(validColumns, col)
		}
	}

	if len(validColumns) == 0 {
		return fmt.Errorf("no compatible columns found between backup and destination table")
	}

	primaryKeys, err := getPrimaryKeys(db, tableData.TableName)
	if err != nil {
		return fmt.Errorf("failed to get primary keys: %w", err)
	}

	uniqueConstraints, err := getUniqueConstraints(db, tableData.TableName)
	if err != nil {
		return fmt.Errorf("failed to get unique constraints: %w", err)
	}

	var query string
	var conflictColumns []string
	var columnsToInsert []string
	var columnsForValues []string
	var columnsList string
	var placeholdersList string

	// Priorizar UNIQUE constraints sobre PRIMARY KEY
	// Las UNIQUE constraints representan la lógica de negocio (cómo se identifica realmente un registro)
	// mientras que PK suele ser solo un ID técnico auto-increment
	if len(uniqueConstraints) > 0 {
		conflictColumns = uniqueConstraints[0]
	} else if len(primaryKeys) > 0 {
		conflictColumns = primaryKeys
	}

	columnsToInsert = validColumns
	columnsForValues = validColumns

	quotedColumnsToInsert := make([]string, len(columnsToInsert))
	for idx, col := range columnsToInsert {
		quotedColumnsToInsert[idx] = fmt.Sprintf(`"%s"`, col)
	}
	columnsList = strings.Join(quotedColumnsToInsert, ", ")

	placeholdersForInsert := make([]string, len(columnsToInsert))
	for idx := range placeholdersForInsert {
		placeholdersForInsert[idx] = fmt.Sprintf("$%d", idx+1)
	}
	placeholdersList = strings.Join(placeholdersForInsert, ", ")

	hasConflict := len(conflictColumns) > 0
	if hasConflict {
		quotedConflictCols := make([]string, len(conflictColumns))
		for idx, col := range conflictColumns {
			quotedConflictCols[idx] = fmt.Sprintf(`"%s"`, col)
		}
		conflictTarget := strings.Join(quotedConflictCols, ", ")

		updateSet := make([]string, 0)
		for _, col := range columnsToInsert {
			// Excluir columnas que son parte del conflicto
			isConflictCol := false
			for _, cc := range conflictColumns {
				if col == cc {
					isConflictCol = true
					break
				}
			}

			// Excluir columnas que son PRIMARY KEY para evitar errores de foreign key
			isPrimaryKey := false
			for _, pk := range primaryKeys {
				if col == pk {
					isPrimaryKey = true
					break
				}
			}

			// Solo actualizar si NO es columna de conflicto Y NO es PRIMARY KEY
			if !isConflictCol && !isPrimaryKey {
				updateSet = append(updateSet, fmt.Sprintf(`"%s" = EXCLUDED."%s"`, col, col))
			}
		}

		if len(updateSet) > 0 {
			query = fmt.Sprintf(
				"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s RETURNING (xmax = 0) AS inserted",
				pq.QuoteIdentifier(tableData.TableName),
				columnsList,
				placeholdersList,
				conflictTarget,
				strings.Join(updateSet, ", "),
			)
		} else {
			query = fmt.Sprintf(
				"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO NOTHING",
				pq.QuoteIdentifier(tableData.TableName),
				columnsList,
				placeholdersList,
				conflictTarget,
			)
		}
	} else {
		query = fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s)",
			pq.QuoteIdentifier(tableData.TableName),
			columnsList,
			placeholdersList,
		)
	}

	for _, row := range tableData.Rows {
		values := make([]any, len(columnsForValues))
		for idx, col := range columnsForValues {
			values[idx] = row[col]
		}

		if hasConflict && strings.Contains(query, "RETURNING") {
			var isInserted bool
			err := db.QueryRow(query, values...).Scan(&isInserted)
			if err != nil {
				return fmt.Errorf("failed to insert row: %w", err)
			}
		} else {
			if _, err := db.Exec(query, values...); err != nil {
				return fmt.Errorf("failed to insert row: %w", err)
			}
		}
	}

	return nil
}

func getPrimaryKeys(db *sql.DB, tableName string) ([]string, error) {
	query := `
		SELECT a.attname
		FROM pg_index i
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = $1::regclass AND i.indisprimary
		ORDER BY a.attnum
	`

	rows, err := db.Query(query, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to query primary keys: %w", err)
	}
	defer rows.Close()

	var pks []string
	for rows.Next() {
		var pk string
		if err := rows.Scan(&pk); err != nil {
			return nil, fmt.Errorf("failed to scan primary key: %w", err)
		}
		pks = append(pks, pk)
	}

	return pks, rows.Err()
}

func getUniqueConstraints(db *sql.DB, tableName string) ([][]string, error) {
	query := `
		SELECT 
			array_agg(a.attname ORDER BY u.attposition)
		FROM (
			SELECT 
				con.conrelid,
				con.conkey,
				unnest(con.conkey) as conkey_elem,
				generate_subscripts(con.conkey, 1) as attposition
			FROM pg_constraint con
			WHERE con.conrelid = $1::regclass 
				AND con.contype = 'u'  -- UNIQUE constraints
		) u
		JOIN pg_attribute a ON a.attrelid = u.conrelid AND a.attnum = u.conkey_elem
		GROUP BY u.conkey
	`

	rows, err := db.Query(query, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to query unique constraints: %w", err)
	}
	defer rows.Close()

	var constraints [][]string
	for rows.Next() {
		var columns []string
		if err := rows.Scan((*pq.StringArray)(&columns)); err != nil {
			return nil, fmt.Errorf("failed to scan unique constraint columns: %w", err)
		}
		constraints = append(constraints, columns)
	}

	return constraints, rows.Err()
}
