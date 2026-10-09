package converters

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	goyaml "gopkg.in/yaml.v3"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/importer"
)

var integrationsConfigDir = "/utmstack/events-engine-workdir/pipeline"

// SetIntegrationsConfigDir overrides the default V12 plugin-config directory.
func SetIntegrationsConfigDir(dir string) {
	if dir != "" {
		integrationsConfigDir = dir
	}
}

// pluginNames mirrors V12's repository.CodeSchemaProvider set: only these
// module families are configurable in V12 and loaded from YAML files.
var pluginNames = map[string]string{
	"AWS_IAM_USER": "aws",
	"AZURE":        "azure",
	"GCP":          "gcp",
	"O365":         "o365",
	"BITDEFENDER":  "bitdefender",
	"CROWDSTRIKE":  "crowdstrike",
	"SOPHOS":       "sophos",
}

func pluginKey(moduleName string) string {
	if name, ok := pluginNames[strings.ToUpper(moduleName)]; ok {
		return name
	}
	return strings.ToLower(moduleName)
}

type tenantConfig struct {
	ID     string        `yaml:"id"`
	Groups []configGroup `yaml:"groups"`
}

type configGroup struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description,omitempty"`
	Config      map[string]string `yaml:"config"`
}

type pluginsFile struct {
	Plugins map[string]pluginEntry `yaml:"plugins"`
}

type pluginEntry struct {
	Tenants []tenantConfig `yaml:"tenants"`
}

type integrationsConverter struct{}

// SrcTable anchors this converter in the cross-table pass; ImportAll
// consumes utm_module, utm_module_group and utm_module_group_configuration.
func (integrationsConverter) SrcTable() string { return "utm_module_group" }

func (integrationsConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	return 0, nil
}

func (c integrationsConverter) ImportAll(ctx context.Context, tables map[string][]map[string]any, destDB *sql.DB) (map[string]int, error) {
	imported := map[string]int{}

	modulesByID := buildModuleNameIndex(tables["utm_module"])
	groups := tables["utm_module_group"]
	confsByGroup := buildGroupConfIndex(tables["utm_module_group_configuration"])

	groupsByPlugin := map[string][]configGroup{}
	migrated := 0

	sorted := append([]map[string]any(nil), groups...)
	sort.Slice(sorted, func(i, j int) bool {
		return fieldString(sorted[i], "id") < fieldString(sorted[j], "id")
	})

	for _, g := range sorted {
		groupID := fieldString(g, "id")
		moduleID := fieldString(g, "module_id")
		moduleName := modulesByID[moduleID]
		if moduleName == "" {
			log.Printf("converter: integrations: group %s (%q) has unknown module_id %s, skipping",
				groupID, fieldString(g, "group_name"), moduleID)
			continue
		}
		if _, ok := pluginNames[strings.ToUpper(moduleName)]; !ok {
			log.Printf("converter: integrations: group %s (%q, module %s) is not a V12 plugin family, skipping",
				groupID, fieldString(g, "group_name"), moduleName)
			continue
		}

		conf := confsByGroup[groupID]
		config := map[string]string{}
		for k, v := range conf {
			if v == "" {
				continue
			}
			config[k] = v
		}

		key := pluginKey(moduleName)
		groupsByPlugin[key] = append(groupsByPlugin[key], configGroup{
			Name:        fieldString(g, "group_name"),
			Description: fieldString(g, "group_description"),
			Config:      config,
		})
		migrated++
	}

	for key, gs := range groupsByPlugin {
		sort.Slice(gs, func(i, j int) bool { return gs[i].Name < gs[j].Name })
		if err := mergeGroupsIntoFile(filepath.Join(integrationsConfigDir, "system_plugins_"+key+".yaml"), key, gs); err != nil {
			return imported, fmt.Errorf("integrations: write %s: %w", key, err)
		}
	}

	if migrated > 0 {
		log.Printf("converter: integrations: wrote %d group config(s) to %s", migrated, integrationsConfigDir)
	}
	imported["utm_module"] = len(modulesByID)
	imported["utm_module_group"] = migrated
	imported["utm_module_group_configuration"] = len(tables["utm_module_group_configuration"])
	return imported, nil
}

// buildModuleNameIndex maps utm_module.id → module_name.
func buildModuleNameIndex(rows []map[string]any) map[string]string {
	idx := map[string]string{}
	for _, r := range rows {
		id := fieldString(r, "id")
		name := fieldString(r, "module_name")
		if id != "" && name != "" {
			idx[id] = name
		}
	}
	return idx
}

// buildGroupConfIndex maps utm_module_group_configuration.group_id →
// conf_key → conf_value. Values are ciphertext for password/file types
// (encrypted at save time in V11 with the same cipher V12 uses); they copy
// verbatim.
func buildGroupConfIndex(rows []map[string]any) map[string]map[string]string {
	idx := map[string]map[string]string{}
	for _, r := range rows {
		gid := fieldString(r, "group_id")
		key := fieldString(r, "conf_key")
		if gid == "" || key == "" {
			continue
		}
		if idx[gid] == nil {
			idx[gid] = map[string]string{}
		}
		idx[gid][key] = fieldString(r, "conf_value")
	}
	return idx
}

func mergeGroupsIntoFile(path, key string, groups []configGroup) error {
	pf := pluginsFile{Plugins: map[string]pluginEntry{}}
	if data, err := os.ReadFile(path); err == nil {
		if err := goyaml.Unmarshal(data, &pf); err != nil {
			return err
		}
		if pf.Plugins == nil {
			pf.Plugins = map[string]pluginEntry{}
		}
	}

	entry, ok := pf.Plugins[key]
	if !ok {
		entry = pluginEntry{Tenants: []tenantConfig{}}
	}

	var existing *tenantConfig
	for i := range entry.Tenants {
		if entry.Tenants[i].ID == DefaultTenantID {
			existing = &entry.Tenants[i]
			break
		}
	}
	if existing == nil {
		entry.Tenants = append(entry.Tenants, tenantConfig{ID: DefaultTenantID})
		existing = &entry.Tenants[len(entry.Tenants)-1]
	}

	existing.Groups = groups
	pf.Plugins[key] = entry

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := goyaml.Marshal(pf)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func init() {
	importer.Register(integrationsConverter{})
}
