package config

import "time"

const (
	MigrationServiceName            = "UTMStackMigrateAgent"
	MigrationServiceDisplayName     = "UTMStack Migrate Agent Service"
	MigrationServiceDescription     = "Monitors and migrates UTMStack Agent from v10 to v11"
	AgentServiceName                = "UTMStackAgent"
	UpdaterServiceName              = "UTMStackUpdater"
	LogsCollectorServiceName        = "UTMStackModulesLogsCollector"
	WindowsLogsCollectorServiceName = "UTMStackWindowsLogsCollector"
	PollInterval                    = 30 * time.Second
	ServiceStopTimeout              = 10 * time.Second
	HealthCheckWait                 = 30 * time.Second
	MinBinarySize                   = 1 * 1024 * 1024
	DownloadMaxRetries              = 3
	MigrationMaxRetries             = 3
)

// PreservedForForwarder are files copied out of the V11 agent dir before it
// is wiped. They belong to the forwarder/collector world, not the V12 agent
// (which never reads them): log-collector-config.json holds the
// integration listener ports and utmstack-collector-mac the macOS collector
// binary. The forwarder install step consumes them from MigrationPath().
var PreservedForForwarder = []string{
	"log-collector-config.json",
	"utmstack-collector-mac",
}
