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
				"utm_integration",
				"utm_integration_conf",
				"utm_server_module",
				"utm_federation_service_client",
			},
		},
		{
			Database: AgentManagerDatabase,
			Tables: []string{
				"agents",
				"collectors",
			},
		},
	}
}
