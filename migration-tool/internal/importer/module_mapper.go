package importer

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

// ModuleMapper handles the mapping of module-related tables between V10 and V11
// Since modules have different IDs between versions and some modules were removed/added,
// we need to map by module_name instead of id
type ModuleMapper struct {
	db *sql.DB
	// Maps V10 module_id -> V11 module_id
	moduleIDMap map[int64]int64
	// Maps V10 group_id -> V11 group_id
	groupIDMap map[int64]int64
	// Modules that exist in V10 but not in V11 (deprecated)
	skippedModules []string
}

// NewModuleMapper creates a new mapper and loads the V11 module data
func NewModuleMapper(db *sql.DB) *ModuleMapper {
	return &ModuleMapper{
		db:             db,
		moduleIDMap:    make(map[int64]int64),
		groupIDMap:     make(map[int64]int64),
		skippedModules: make([]string, 0),
	}
}

// getV11ModuleByName finds a module in V11 by its name and returns its ID
func (m *ModuleMapper) getV11ModuleByName(moduleName string) (int64, error) {
	var id int64
	err := m.db.QueryRow(
		"SELECT id FROM utm_module WHERE module_name = $1",
		moduleName,
	).Scan(&id)

	if err == sql.ErrNoRows {
		return 0, nil // Module doesn't exist in V11
	}
	if err != nil {
		return 0, fmt.Errorf("failed to query module %s: %w", moduleName, err)
	}
	return id, nil
}

// ImportModules handles utm_module import with name-based mapping
// Instead of inserting with V10 IDs, it updates existing V11 modules by name
func (m *ModuleMapper) ImportModules(tableData *domain.TableData) error {
	if len(tableData.Rows) == 0 {
		return nil
	}

	ui.ClearLine()
	ui.StartOperation("Mapping modules by name (V10 → V11)")

	updateQuery := `
		UPDATE utm_module 
		SET module_active = $1, 
		    module_category = $2,
		    module_description = $3,
		    pretty_name = $4,
		    module_icon = $5,
		    needs_restart = $6,
		    lite_version = $7,
		    is_activatable = $8
		WHERE module_name = $9
		RETURNING id
	`

	updated := 0
	skipped := 0

	for _, row := range tableData.Rows {
		moduleName, _ := row["module_name"].(string)
		v10ID := toInt64(row["id"])

		// Check if module exists in V11
		v11ID, err := m.getV11ModuleByName(moduleName)
		if err != nil {
			return err
		}

		if v11ID == 0 {
			// Module doesn't exist in V11 (deprecated)
			m.skippedModules = append(m.skippedModules, moduleName)
			skipped++
			continue
		}

		// Store the mapping V10 ID -> V11 ID
		m.moduleIDMap[v10ID] = v11ID

		// Update the module in V11 with V10 settings
		var returnedID int64
		err = m.db.QueryRow(
			updateQuery,
			row["module_active"],
			row["module_category"],
			row["module_description"],
			row["pretty_name"],
			row["module_icon"],
			row["needs_restart"],
			row["lite_version"],
			row["is_activatable"],
			moduleName,
		).Scan(&returnedID)

		if err != nil {
			return fmt.Errorf("failed to update module %s: %w", moduleName, err)
		}
		updated++
	}

	ui.Success(fmt.Sprintf("Modules: %d updated, %d skipped (not in V11)", updated, skipped))
	if len(m.skippedModules) > 0 {
		ui.Detail("Skipped", fmt.Sprintf("%v", m.skippedModules))
	}

	return nil
}

// ImportModuleGroups handles utm_module_group import with ID remapping
func (m *ModuleMapper) ImportModuleGroups(tableData *domain.TableData) error {
	if len(tableData.Rows) == 0 {
		return nil
	}

	ui.ClearLine()
	ui.StartOperation("Importing module groups with ID remapping")

	// First, let's check what groups already exist in V11 by (module_id, group_name)
	// Use temporary map to ensure atomicity - only assign to real map if all rows scan successfully
	tempExistingGroups := make(map[string]int64) // "module_id:group_name" -> group_id
	rows, err := m.db.Query("SELECT id, module_id, group_name FROM utm_module_group")
	if err != nil {
		return fmt.Errorf("failed to query existing groups: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, moduleID int64
		var groupName string
		if err := rows.Scan(&id, &moduleID, &groupName); err != nil {
			return fmt.Errorf("failed to scan existing group: %w", err)
		}
		key := fmt.Sprintf("%d:%s", moduleID, groupName)
		tempExistingGroups[key] = id
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating existing groups: %w", err)
	}

	// All rows scanned successfully, now use the data
	existingGroups := tempExistingGroups

	inserted := 0
	updated := 0
	skipped := 0

	for _, row := range tableData.Rows {
		v10ModuleID := toInt64(row["module_id"])
		v10GroupID := toInt64(row["id"])
		groupName, _ := row["group_name"].(string)

		// Get the V11 module ID
		v11ModuleID, exists := m.moduleIDMap[v10ModuleID]
		if !exists {
			// Module was skipped (deprecated), skip its groups too
			skipped++
			continue
		}

		key := fmt.Sprintf("%d:%s", v11ModuleID, groupName)

		if existingGroupID, exists := existingGroups[key]; exists {
			// Group already exists, update it and store mapping
			m.groupIDMap[v10GroupID] = existingGroupID

			_, err := m.db.Exec(`
				UPDATE utm_module_group 
				SET group_description = $1, collector_id = $2
				WHERE id = $3
			`, row["group_description"], row["collector_id"], existingGroupID)

			if err != nil {
				return fmt.Errorf("failed to update group %s: %w", groupName, err)
			}
			updated++
		} else {
			// Group doesn't exist, insert it
			var newID int64
			err := m.db.QueryRow(`
				INSERT INTO utm_module_group (module_id, group_name, group_description, collector_id)
				VALUES ($1, $2, $3, $4)
				RETURNING id
			`, v11ModuleID, groupName, row["group_description"], row["collector_id"]).Scan(&newID)

			if err != nil {
				return fmt.Errorf("failed to insert group %s: %w", groupName, err)
			}

			m.groupIDMap[v10GroupID] = newID
			inserted++
		}
	}

	ui.Success(fmt.Sprintf("Module groups: %d inserted, %d updated, %d skipped", inserted, updated, skipped))
	return nil
}

// ImportModuleGroupConfigurations handles utm_module_group_configuration with ID remapping
func (m *ModuleMapper) ImportModuleGroupConfigurations(tableData *domain.TableData) error {
	if len(tableData.Rows) == 0 {
		return nil
	}

	ui.ClearLine()
	ui.StartOperation("Importing module group configurations with ID remapping")

	inserted := 0
	updated := 0
	skipped := 0

	for _, row := range tableData.Rows {
		v10GroupID := toInt64(row["group_id"])
		confKey, _ := row["conf_key"].(string)

		// Get the V11 group ID
		v11GroupID, exists := m.groupIDMap[v10GroupID]
		if !exists {
			// Group was skipped (deprecated module), skip its configs too
			skipped++
			continue
		}

		// Check if config already exists
		var existingID int64
		err := m.db.QueryRow(
			"SELECT id FROM utm_module_group_configuration WHERE group_id = $1 AND conf_key = $2",
			v11GroupID, confKey,
		).Scan(&existingID)

		if err == sql.ErrNoRows {
			// Insert new config
			_, err = m.db.Exec(`
				INSERT INTO utm_module_group_configuration 
				(group_id, conf_key, conf_value, conf_name, conf_description, conf_data_type, conf_required, conf_options)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			`,
				v11GroupID,
				confKey,
				row["conf_value"],
				row["conf_name"],
				row["conf_description"],
				row["conf_data_type"],
				row["conf_required"],
				row["conf_options"],
			)
			if err != nil {
				return fmt.Errorf("failed to insert config %s: %w", confKey, err)
			}
			inserted++
		} else if err != nil {
			return fmt.Errorf("failed to check existing config: %w", err)
		} else {
			// Update existing config
			_, err = m.db.Exec(`
				UPDATE utm_module_group_configuration 
				SET conf_value = $1, conf_name = $2, conf_description = $3, 
				    conf_data_type = $4, conf_required = $5, conf_options = $6
				WHERE id = $7
			`,
				row["conf_value"],
				row["conf_name"],
				row["conf_description"],
				row["conf_data_type"],
				row["conf_required"],
				row["conf_options"],
				existingID,
			)
			if err != nil {
				return fmt.Errorf("failed to update config %s: %w", confKey, err)
			}
			updated++
		}
	}

	ui.Success(fmt.Sprintf("Module configs: %d inserted, %d updated, %d skipped", inserted, updated, skipped))
	return nil
}

// Helper function to convert any to int64
func toInt64(v any) int64 {
	switch val := v.(type) {
	case int:
		return int64(val)
	case int64:
		return val
	case float64:
		return int64(val)
	case string:
		if val == "" {
			return 0
		}
		parsed, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			log.Printf("Warning: toInt64 failed to parse string %q: %v", val, err)
			return 0
		}
		return parsed
	case nil:
		return 0
	default:
		log.Printf("Warning: toInt64 received unhandled type %T with value %v", v, v)
		return 0
	}
}
