package importer

import (
	"context"
	"database/sql"
	"fmt"
	"log"
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

// Result is the per-table outcome of an import.
type Result struct {
	Imported map[string]int
	Skipped  []string
}

func (r Result) PrintSummary() {
	ui.Message("Import summary: %d tables imported, %d skipped", len(r.Imported), len(r.Skipped))
	if len(r.Skipped) > 0 {
		ui.Message("skipped (no converter): %s", strings.Join(r.Skipped, ", "))
	}
}

func (i *Importer) Import(inputFile string) (Result, error) {
	result := Result{Imported: map[string]int{}}
	ctx := context.Background()

	backup, err := readFromFile(inputFile)
	if err != nil {
		return result, fmt.Errorf("failed to read backup file: %w", err)
	}

	if len(backup.Databases) == 0 && len(backup.Tables) > 0 {
		backup.Databases = []*domain.DatabaseBackup{
			{
				Database: config.DefaultDatabase,
				Tables:   backup.Tables,
			},
		}
	} else if len(backup.Databases) == 0 {
		return result, fmt.Errorf("backup file contains no data")
	}

	if backup.InternalKey != "" && i.instanceCfg.SSHUser == "" {
		if err := yaml.WriteInternalKey(config.UTMStackYMLPath, backup.InternalKey); err != nil {
			return result, fmt.Errorf("failed to write internal key: %w", err)
		}
	}

	ui.Info("Starting data import")

	// Initialize progress bar
	i.progress = ui.NewProgress(i.totalTables, "Importing tables")

	consumed := make(map[string]bool)

	// Open every destination database up front: cross-database converters
	// need more than one connection at a time.
	dbConns := make(map[string]*sql.DB, len(backup.Databases))
	defer func() {
		for _, db := range dbConns {
			db.Close()
		}
	}()
	for _, dbBackup := range backup.Databases {
		db, err := postgres.Connect(i.instanceCfg, dbBackup.Database)
		if err != nil {
			return result, fmt.Errorf("failed to connect to database %s: %w", dbBackup.Database, err)
		}
		dbConns[dbBackup.Database] = db
	}

	// Cross-database converters run first, with access to all connections.
	for _, dbBackup := range backup.Databases {
		tables := make(map[string][]map[string]any, len(dbBackup.Tables))
		for _, td := range dbBackup.Tables {
			tables[td.TableName] = td.Rows
		}
		for _, td := range dbBackup.Tables {
			if consumed[td.TableName] {
				continue
			}
			conv, ok := Get(td.TableName)
			if !ok {
				continue
			}
			cross, ok := conv.(CrossDatabaseConverter)
			if !ok {
				continue
			}
			imported, err := cross.ImportAll(ctx, tables, dbConns)
			if err != nil {
				return result, fmt.Errorf("failed to import table %s: %w", td.TableName, err)
			}
			for t, n := range imported {
				result.Imported[t] = n
				consumed[t] = true
				i.progress.IncrementWithLabel(t)
			}
		}
	}

	for _, dbBackup := range backup.Databases {
		db := dbConns[dbBackup.Database]

		tables := make(map[string][]map[string]any, len(dbBackup.Tables))
		order := make([]string, 0, len(dbBackup.Tables))
		for _, td := range dbBackup.Tables {
			tables[td.TableName] = td.Rows
			order = append(order, td.TableName)
		}

		// Cross-table converters see the whole database slice so a
		// converter can join its primary table with sibling tables.
		for _, name := range order {
			conv, ok := Get(name)
			if !ok || consumed[name] {
				continue
			}
			cross, ok := conv.(CrossTableConverter)
			if !ok {
				continue
			}
			imported, err := cross.ImportAll(ctx, tables, db)
			if err != nil {
				return result, fmt.Errorf("failed to import table %s: %w", name, err)
			}
			for t, n := range imported {
				result.Imported[t] = n
				consumed[t] = true
				i.progress.IncrementWithLabel(t)
			}
		}

		for _, name := range order {
			if consumed[name] {
				continue
			}
			conv, ok := Get(name)
			if !ok {
				log.Printf("WARNING: no converter for table %s - data NOT imported", name)
				result.Skipped = append(result.Skipped, name)
				i.progress.IncrementWithLabel(name)
				continue
			}
			n, err := conv.Import(ctx, tables[name], db)
			if err != nil {
				return result, fmt.Errorf("failed to import table %s: %w", name, err)
			}
			result.Imported[name] = n
			i.progress.IncrementWithLabel(name)
		}
	}

	i.progress.FinishWithMessage("Import completed")
	return result, nil
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

func (i *Importer) importGeneric(db *sql.DB, tableData *domain.TableData) error {
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

	// UNIQUE constraints identify a record by business key; the PK is a
	// technical surrogate and loses that meaning, so UNIQUE wins.
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
			isConflictCol := false
			for _, cc := range conflictColumns {
				if col == cc {
					isConflictCol = true
					break
				}
			}

			isPrimaryKey := false
			for _, pk := range primaryKeys {
				if col == pk {
					isPrimaryKey = true
					break
				}
			}

			// Never touch conflict columns or the PK in the UPDATE SET: it would
			// violate the uniqueness constraint or break foreign keys.
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
