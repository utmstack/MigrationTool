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
