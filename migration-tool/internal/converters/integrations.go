package converters

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
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

var pluginNames = map[string]string{
	"AWS_IAM_USER": "aws",
	"AZURE":        "azure",
	"GCP":          "gcp",
	"O365":         "o365",
	"BITDEFENDER":  "bitdefender",
	"CROWDSTRIKE":  "crowdstrike",
	"SOPHOS":       "sophos",
}

func pluginKey(integration string) string {
	if name, ok := pluginNames[strings.ToUpper(integration)]; ok {
		return name
	}
	return strings.ToLower(integration)
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

func (integrationsConverter) SrcTable() string { return "utm_integration" }

func (c integrationsConverter) Import(ctx context.Context, rows []map[string]interface{}, destDB *sql.DB) (int, error) {
	return 0, nil
}

func (c integrationsConverter) ImportAll(ctx context.Context, tables map[string][]map[string]any, destDB *sql.DB) (map[string]int, error) {
	imported := map[string]int{}

	integrations := tables["utm_integration"]
	confByIntegration := buildConfIndex(tables["utm_integration_conf"])
	moduleNameByID := buildModuleIndex(tables["utm_server_module"])

	groupsByPlugin := map[string][]configGroup{}
	count := 0
	confCount := 0

	for _, src := range integrations {
		integrationName := fieldString(src, "integration_name")
		if integrationName == "" {
			continue
		}
		moduleID := fieldString(src, "module_id")
		moduleName := moduleNameByID[moduleID]
		if moduleName == "" {
			log.Printf("converter: integrations: integration %q has unknown module_id %s, skipping", integrationName, moduleID)
			continue
		}

		desc := fieldString(src, "integration_description")
		config := confByIntegration[fieldString(src, "id")]
		if config == nil {
			config = map[string]string{}
		}
		for k, v := range config {
			if v == "*****" {
				config[k] = ""
			}
		}

		key := pluginKey(moduleName)
		groupsByPlugin[key] = append(groupsByPlugin[key], configGroup{
			Name:        integrationName,
			Description: desc,
			Config:      config,
		})
		confCount += len(config)
		count++
	}

	for key, groups := range groupsByPlugin {
		if err := mergeGroupsIntoFile(filepath.Join(integrationsConfigDir, "system_plugins_"+key+".yaml"), key, groups); err != nil {
			return imported, fmt.Errorf("integrations: write %s: %w", key, err)
		}
	}

	if count > 0 {
		log.Printf("converter: integrations: wrote %d integration config group(s) to %s", count, integrationsConfigDir)
	}
	imported["utm_integration"] = count
	imported["utm_integration_conf"] = confCount
	imported["utm_server_module"] = len(moduleNameByID)
	return imported, nil
}

func buildConfIndex(rows []map[string]any) map[string]map[string]string {
	idx := map[string]map[string]string{}
	for _, r := range rows {
		id := fieldString(r, "integration_id")
		short := fieldString(r, "conf_short")
		if id == "" || short == "" {
			continue
		}
		if idx[id] == nil {
			idx[id] = map[string]string{}
		}
		idx[id][short] = fieldString(r, "conf_value")
	}
	return idx
}

func buildModuleIndex(rows []map[string]any) map[string]string {
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
