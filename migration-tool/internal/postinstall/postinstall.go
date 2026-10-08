package postinstall

import (
	"database/sql"
	"log"
)

// RunPostInstallQueries is a no-op for V12: the V11-specific activation
// queries (utm_menu, utm_logstash_filter, utm_index_pattern) target tables
// that do not exist in V12.
func RunPostInstallQueries(db *sql.DB) error {
	log.Println("no post-install queries required for V12")
	return nil
}
