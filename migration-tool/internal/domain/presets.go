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
				"utm_dashboard",
				"utm_visualization",
				"utm_dashboard_visualization",
				"utm_integration",
				"utm_integration_conf",
				"utm_server_module",
				"utm_module",
				"utm_module_group",
				"utm_module_group_configuration",
				"utm_network_scan",
				"utm_asset_group",
				"utm_collectors",
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
