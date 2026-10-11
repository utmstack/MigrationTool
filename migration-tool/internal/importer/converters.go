package importer

import (
	"context"
	"database/sql"
)

// Converter maps the rows of one V11 source table into the V12 destination schema.
type Converter interface {
	SrcTable() string
	Import(ctx context.Context, rows []map[string]any, destDB *sql.DB) (int, error)
}

// CrossTableConverter is a Converter that also needs sibling tables from the
// backup. It is called once per SrcTable, but can read other tables' rows
// (e.g. utm_integration needs utm_integration_conf + utm_server_module).
type CrossTableConverter interface {
	Converter
	ImportAll(ctx context.Context, tables map[string][]map[string]any, destDB *sql.DB) (map[string]int, error)
}

// CrossDatabaseConverter is a Converter whose source table and destination
// table live in different databases (e.g. utmstack.utm_federation_service_client
// → agentmanager.connection_keys). It is called once, after every database in
// the backup has been connected, so it can write to the right one.
type CrossDatabaseConverter interface {
	Converter
	ImportAll(ctx context.Context, tables map[string][]map[string]any, dbs map[string]*sql.DB) (map[string]int, error)
}

var registry = map[string]Converter{}

// Register panics on a duplicate SrcTable so a wiring mistake surfaces at process start.
func Register(c Converter) {
	if _, dup := registry[c.SrcTable()]; dup {
		panic("duplicate converter for table " + c.SrcTable())
	}
	registry[c.SrcTable()] = c
}

func Has(srcTable string) bool {
	_, ok := registry[srcTable]
	return ok
}

func Get(srcTable string) (Converter, bool) {
	c, ok := registry[srcTable]
	return c, ok
}
