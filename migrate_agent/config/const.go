package config

import "time"

const (
	MigrationServiceName            = "UTMStackMigration"
	MigrationServiceDisplayName     = "UTMStack Migration Service"
	MigrationServiceDescription     = "Monitors and migrates UTMStack Agent from v10 to v11"
	AgentServiceName                = "UTMStackAgent"
	LogsCollectorServiceName        = "UTMStackModulesLogsCollector"
	WindowsLogsCollectorServiceName = "UTMStackWindowsLogsCollector"
	PollInterval                    = 30 * time.Second
	ServiceStopTimeout              = 10 * time.Second
	HealthCheckWait                 = 30 * time.Second
	MinBinarySize                   = 1 * 1024 * 1024
	DownloadMaxRetries              = 3
	MigrationMaxRetries             = 3
)

var PreservedFiles = []string{
	"config.yml",
	"log-collector-config.json",
	"version.json",
	"uuid.yml",
}
