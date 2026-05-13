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
				"jhi_authority",
				"jhi_user",
				"jhi_user_authority",

				"utm_configuration_parameter",
				"utm_configuration_section",

				"utm_module",
				"utm_module_group",
				"utm_module_group_configuration",

				"utm_integration",
				"utm_integration_conf",
				"utm_collectors",
			},
		},
		{
			Database: AgentManagerDatabase,
			Tables: []string{
				"agents",
				"last_seens",
				"collectors",
			},
		},
	}
}
