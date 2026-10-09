package converters

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	goyaml "gopkg.in/yaml.v3"
)

func TestPluginKey(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"O365", "o365"},
		{"o365", "o365"},
		{"AZURE", "azure"},
		{"AWS_IAM_USER", "aws"},
		{"BITDEFENDER", "bitdefender"},
		{"SOMETHING_ELSE", "something_else"},
	}
	for _, c := range cases {
		if got := pluginKey(c.in); got != c.want {
			t.Errorf("pluginKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func runIntegrations(t *testing.T, dir string) {
	t.Helper()
	old := integrationsConfigDir
	integrationsConfigDir = dir
	t.Cleanup(func() { integrationsConfigDir = old })

	tables := map[string][]map[string]any{
		"utm_integration": {
			{"id": "10", "module_id": "100", "integration_name": "Office Group A", "integration_description": "o365 team a", "integration_icon_path": ""},
			{"id": "11", "module_id": "101", "integration_name": "Azure Tenant B", "integration_description": "az tenant", "integration_icon_path": ""},
			{"id": "12", "module_id": "999", "integration_name": "Orphan", "integration_description": "", "integration_icon_path": ""},
			{"id": "13", "module_id": "100", "integration_name": "Office Group B", "integration_description": "", "integration_icon_path": ""},
		},
		"utm_integration_conf": {
			{"id": "1", "integration_id": "10", "conf_short": "client_id", "conf_large": "Client ID", "conf_description": "", "conf_value": "abc123", "conf_datatype": "text"},
			{"id": "2", "integration_id": "10", "conf_short": "client_secret", "conf_large": "Secret", "conf_description": "", "conf_value": "V11CIPHERTEXT==", "conf_datatype": "password"},
			{"id": "3", "integration_id": "10", "conf_short": "other_secret", "conf_large": "Other", "conf_description": "", "conf_value": "*****", "conf_datatype": "password"},
			{"id": "4", "integration_id": "11", "conf_short": "tenant_id", "conf_large": "Tenant", "conf_description": "", "conf_value": "az-tenant", "conf_datatype": "text"},
		},
		"utm_server_module": {
			{"id": "100", "module_name": "O365", "pretty_name": "Office 365"},
			{"id": "101", "module_name": "AZURE", "pretty_name": "Azure"},
		},
	}

	_, err := integrationsConverter{}.ImportAll(context.Background(), tables, nil)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
}

func TestImportAllWritesYAMLPerPlugin(t *testing.T) {
	dir := t.TempDir()
	runIntegrations(t, dir)

	o365Path := filepath.Join(dir, "system_plugins_o365.yaml")
	azurePath := filepath.Join(dir, "system_plugins_azure.yaml")

	if _, err := os.Stat(o365Path); err != nil {
		t.Fatalf("expected %s: %v", o365Path, err)
	}
	if _, err := os.Stat(azurePath); err != nil {
		t.Fatalf("expected %s: %v", azurePath, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "system_plugins_unknown.yaml")); !os.IsNotExist(err) {
		t.Errorf("orphan module should not produce a file, stat err=%v", err)
	}

	var o365 pluginsFile
	data, err := os.ReadFile(o365Path)
	if err != nil {
		t.Fatalf("read o365 yaml: %v", err)
	}
	if err := goyaml.Unmarshal(data, &o365); err != nil {
		t.Fatalf("parse o365 yaml: %v", err)
	}

	entry, ok := o365.Plugins["o365"]
	if !ok {
		t.Fatalf("o365 key missing in plugins: %v", o365.Plugins)
	}
	if len(entry.Tenants) != 1 || entry.Tenants[0].ID != DefaultTenantID {
		t.Errorf("tenants = %+v, want one default tenant", entry.Tenants)
	}
	if len(entry.Tenants[0].Groups) != 2 {
		t.Fatalf("groups = %d, want 2 (orphan module row excluded)", len(entry.Tenants[0].Groups))
	}

	g0 := entry.Tenants[0].Groups[0]
	if g0.Name != "Office Group A" {
		t.Errorf("group name = %q", g0.Name)
	}
	if g0.Config["client_id"] != "abc123" {
		t.Errorf("client_id = %q", g0.Config["client_id"])
	}
	if g0.Config["client_secret"] != "V11CIPHERTEXT==" {
		t.Errorf("ciphertext not copied verbatim: %q", g0.Config["client_secret"])
	}
	if g0.Config["other_secret"] != "" {
		t.Errorf("***** mask should become empty, got %q", g0.Config["other_secret"])
	}
}

func TestImportAllMergePreservesExistingFile(t *testing.T) {
	dir := t.TempDir()

	existing := pluginsFile{Plugins: map[string]pluginEntry{
		"o365": {Tenants: []tenantConfig{{ID: "11111111-1111-1111-1111-111111111111", Groups: []configGroup{{Name: "kept", Config: map[string]string{"a": "1"}}}}}},
	}}
	data, _ := goyaml.Marshal(existing)
	if err := os.WriteFile(filepath.Join(dir, "system_plugins_o365.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	runIntegrations(t, dir)

	var out pluginsFile
	raw, err := os.ReadFile(filepath.Join(dir, "system_plugins_o365.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := goyaml.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse merged yaml: %v", err)
	}
	entry := out.Plugins["o365"]
	if len(entry.Tenants) != 2 {
		t.Fatalf("tenants = %d, want 2 (existing kept + default added)", len(entry.Tenants))
	}
	var defaultTenant, keptTenant *tenantConfig
	for i := range entry.Tenants {
		switch entry.Tenants[i].ID {
		case DefaultTenantID:
			defaultTenant = &entry.Tenants[i]
		case "11111111-1111-1111-1111-111111111111":
			keptTenant = &entry.Tenants[i]
		}
	}
	if defaultTenant == nil || keptTenant == nil {
		t.Fatalf("both tenants must survive the merge: %+v", entry.Tenants)
	}
	if len(keptTenant.Groups) != 1 || keptTenant.Groups[0].Name != "kept" {
		t.Errorf("existing tenant groups changed: %+v", keptTenant.Groups)
	}
	if len(defaultTenant.Groups) != 2 {
		t.Errorf("default tenant should carry the 2 o365 groups, got %d", len(defaultTenant.Groups))
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
