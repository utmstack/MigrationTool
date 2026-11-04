package domain

const (
	DefaultDatabase      = "utmstack"
	AgentManagerDatabase = "agentmanager"
)

func GetDefaultMigrationTables() []DatabaseSource {
	return []DatabaseSource{
		{
			Database: DefaultDatabase,
			Tables: []string{
				"jhi_user",
				"jhi_authority",
				"jhi_user_authority",
				"utm_configuration_parameter",
				"utm_module",
				"utm_module_group",
				"utm_module_group_configuration",
				"utm_federation_service_client",
				"utm_alert_tag",
				"utm_alert_tag_rule",
				"utm_incident",
				"utm_incident_alert",
				"utm_incident_history",
				"utm_incident_note",
			},
		},
		// Kept so the agentmanager database is opened during import: the
		// federation converter writes connection_keys into it.
		{
			Database: AgentManagerDatabase,
			Tables:   []string{},
		},
	}
}
