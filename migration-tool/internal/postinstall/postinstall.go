package postinstall

import (
	"database/sql"
	"fmt"
)

func RunPostInstallQueries(db *sql.DB) error {
	// Use EXISTS for efficient check instead of loop iteration
	var hasActiveModules bool
	err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM utm_module WHERE module_active = true)`).Scan(&hasActiveModules)
	if err != nil {
		return fmt.Errorf("failed to check for active modules: %w", err)
	}

	if !hasActiveModules {
		return nil
	}

	menuUpdateQuery := `
		UPDATE utm_menu 
		SET menu_active = true 
		WHERE module_name_short IN (
			SELECT DISTINCT module_name 
			FROM utm_module 
			WHERE module_active = true
		) AND menu_active = false
	`

	_, err = db.Exec(menuUpdateQuery)
	if err != nil {
		return fmt.Errorf("failed to update utm_menu: %w", err)
	}

	filterUpdateQuery := `
		UPDATE utm_logstash_filter 
		SET is_active = true 
		WHERE module_name IN (
			SELECT DISTINCT module_name 
			FROM utm_module 
			WHERE module_active = true
		) AND is_active = false
	`

	_, err = db.Exec(filterUpdateQuery)
	if err != nil {
		return fmt.Errorf("failed to update utm_logstash_filter: %w", err)
	}

	indexPatternUpdateQuery := `
		UPDATE utm_index_pattern 
		SET is_active = true 
		WHERE pattern_module IN (
			SELECT DISTINCT module_name 
			FROM utm_module 
			WHERE module_active = true
		) AND is_active = false
	`

	_, err = db.Exec(indexPatternUpdateQuery)
	if err != nil {
		return fmt.Errorf("failed to update utm_index_pattern: %w", err)
	}

	return nil
}
