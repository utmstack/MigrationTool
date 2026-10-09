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
		{"GCP", "gcp"},
		{"BITDEFENDER", "bitdefender"},
		{"CROWDSTRIKE", "crowdstrike"},
		{"SOPHOS", "sophos"},
		{"CISCO", "cisco"}, // not a plugin family — still lowercased for the skip-log
	}
	for _, c := range cases {
		if got := pluginKey(c.in); got != c.want {
			t.Errorf("pluginKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// fixture mirrors the live V11 shape: utm_module catalog + configured groups.
func fixtureTables() map[string][]map[string]any {
	return map[string][]map[string]any{
		"utm_module": {
			{"id": "34", "module_name": "O365", "pretty_name": "Microsoft 365", "module_category": "Cloud", "server_id": "1", "module_active": "true"},
			{"id": "37", "module_name": "GCP", "pretty_name": "Google Cloud Platform", "module_category": "Cloud", "server_id": "1", "module_active": "false"},
			{"id": "22", "module_name": "CISCO", "pretty_name": "Cisco ASA", "module_category": "Device", "server_id": "1", "module_active": "true"},
			{"id": "999", "module_name": "CROWDSTRIKE", "pretty_name": "CrowdStrike", "module_category": "Device", "server_id": "1", "module_active": "true"},
		},
		"utm_module_group": {
			{"id": "7", "module_id": "34", "group_name": "asas", "group_description": "asas"},
			{"id": "8", "module_id": "37", "group_name": "testTW", "group_description": ""},
			{"id": "9", "module_id": "22", "group_name": "asa-fw", "group_description": "cisco device group (not a V12 plugin)"},
			{"id": "10", "module_id": "999", "group_name": "cs-fleet", "group_description": ""},
			{"id": "11", "module_id": "4242", "group_name": "orphan", "group_description": "unknown module"},
		},
		"utm_module_group_configuration": {
			{"id": "37", "group_id": "7", "conf_key": "office365_client_id", "conf_data_type": "text", "conf_required": "true", "conf_value": "72d16d1f-1353-4a0d-bb7b-455bf7f4b367"},
			{"id": "38", "group_id": "7", "conf_key": "office365_client_secret", "conf_data_type": "password", "conf_required": "true", "conf_value": "V11CIPHERTEXT=="},
			{"id": "39", "group_id": "7", "conf_key": "office365_tenant_id", "conf_data_type": "text", "conf_required": "true", "conf_value": "5a688d77-f830-467b-861a-cfc16370e553"},
			{"id": "40", "group_id": "7", "conf_key": "office365_cloud_environment", "conf_data_type": "select", "conf_required": "false", "conf_value": "Commercial"},
			{"id": "41", "group_id": "8", "conf_key": "jsonKey", "conf_data_type": "file", "conf_required": "true", "conf_value": ""},
			{"id": "42", "group_id": "8", "conf_key": "projectId", "conf_data_type": "text", "conf_required": "true", "conf_value": ""},
			{"id": "43", "group_id": "8", "conf_key": "subscription", "conf_data_type": "text", "conf_required": "true", "conf_value": ""},
			{"id": "44", "group_id": "8", "conf_key": "topic", "conf_data_type": "text", "conf_required": "true", "conf_value": ""},
			{"id": "50", "group_id": "9", "conf_key": "some_device_conf", "conf_data_type": "text", "conf_required": "true", "conf_value": "should-not-migrate"},
			{"id": "51", "group_id": "10", "conf_key": "crowdstrike_client_id", "conf_data_type": "text", "conf_required": "true", "conf_value": "cs-client"},
			{"id": "52", "group_id": "10", "conf_key": "crowdstrike_client_secret", "conf_data_type": "password", "conf_required": "true", "conf_value": "CSCIPHER=="},
		},
	}
}

func runImportAll(t *testing.T, dir string) map[string]int {
	t.Helper()
	old := integrationsConfigDir
	integrationsConfigDir = dir
	t.Cleanup(func() { integrationsConfigDir = old })

	imported, err := integrationsConverter{}.ImportAll(context.Background(), fixtureTables(), nil)
	if err != nil {
		t.Fatalf("ImportAll: %v", err)
	}
	return imported
}

func readYAML(t *testing.T, path string) pluginsFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var pf pluginsFile
	if err := goyaml.Unmarshal(data, &pf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return pf
}

func TestImportAllWritesYAMLPerPlugin(t *testing.T) {
	dir := t.TempDir()
	imported := runImportAll(t, dir)

	if imported["utm_module_group"] != 3 {
		t.Errorf("migrated groups = %d, want 3 (O365 + GCP + CROWDSTRIKE)", imported["utm_module_group"])
	}

	// O365
	pf := readYAML(t, filepath.Join(dir, "system_plugins_o365.yaml"))
	entry, ok := pf.Plugins["o365"]
	if !ok {
		t.Fatalf("o365 plugin missing: %v", pf.Plugins)
	}
	if len(entry.Tenants) != 1 || entry.Tenants[0].ID != DefaultTenantID {
		t.Fatalf("tenants = %+v, want one default tenant", entry.Tenants)
	}
	if len(entry.Tenants[0].Groups) != 1 || entry.Tenants[0].Groups[0].Name != "asas" {
		t.Fatalf("groups = %+v, want one group 'asas'", entry.Tenants[0].Groups)
	}
	cfg := entry.Tenants[0].Groups[0].Config
	if cfg["office365_client_id"] != "72d16d1f-1353-4a0d-bb7b-455bf7f4b367" {
		t.Errorf("client_id = %q", cfg["office365_client_id"])
	}
	if cfg["office365_client_secret"] != "V11CIPHERTEXT==" {
		t.Errorf("client_secret not copied verbatim: %q", cfg["office365_client_secret"])
	}
	if cfg["office365_cloud_environment"] != "Commercial" {
		t.Errorf("cloud_environment = %q", cfg["office365_cloud_environment"])
	}

	// GCP — empty values dropped, non-schema 'topic' copied verbatim
	pf = readYAML(t, filepath.Join(dir, "system_plugins_gcp.yaml"))
	entry, ok = pf.Plugins["gcp"]
	if !ok {
		t.Fatalf("gcp plugin missing: %v", pf.Plugins)
	}
	if len(entry.Tenants[0].Groups) != 1 || entry.Tenants[0].Groups[0].Name != "testTW" {
		t.Fatalf("gcp groups = %+v, want 'testTW'", entry.Tenants[0].Groups)
	}
	if len(entry.Tenants[0].Groups[0].Config) != 0 {
		t.Errorf("gcp config should be empty (all values blank), got %v", entry.Tenants[0].Groups[0].Config)
	}

	// CROWDSTRIKE
	pf = readYAML(t, filepath.Join(dir, "system_plugins_crowdstrike.yaml"))
	entry, ok = pf.Plugins["crowdstrike"]
	if !ok {
		t.Fatalf("crowdstrike plugin missing: %v", pf.Plugins)
	}
	if entry.Tenants[0].Groups[0].Config["crowdstrike_client_secret"] != "CSCIPHER==" {
		t.Errorf("crowdstrike secret = %q", entry.Tenants[0].Groups[0].Config["crowdstrike_client_secret"])
	}

	// CISCO (device group) and orphan must not produce files
	if _, err := os.Stat(filepath.Join(dir, "system_plugins_cisco.yaml")); !os.IsNotExist(err) {
		t.Errorf("non-plugin module should not produce a yaml file, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "system_plugins_orphan.yaml")); !os.IsNotExist(err) {
		t.Errorf("orphan module group should not produce a yaml file, stat err=%v", err)
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

	runImportAll(t, dir)

	out := readYAML(t, filepath.Join(dir, "system_plugins_o365.yaml"))
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
	if len(defaultTenant.Groups) != 1 || defaultTenant.Groups[0].Name != "asas" {
		t.Errorf("default tenant should carry the O365 group, got %+v", defaultTenant.Groups)
	}
}
