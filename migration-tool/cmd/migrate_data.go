package cmd

import (
	"fmt"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/domain"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/searchdata"
)

// newSearchPhases builds the OpenSearch->ClickHouse phases for a migrate run.
// The source executor is required now (the dump runs against it); the
// destination executor is attached later, once the destination SSH is up.
func newSearchPhases(cfg *domain.MigrationConfig, sourceSSH *remote.SSHClient) (*searchdata.Phases, error) {
	if sourceSSH == nil {
		return nil, fmt.Errorf("--with-data requires SSH access to the source instance")
	}
	return searchdata.NewPhases(searchdata.Config{
		SourceHost:    cfg.Source.Host,
		DestHost:      cfg.Destination.Host,
		SourceExec:    sourceSSH,
		DestExec:      nil,
		SourceSSHUser: cfg.Source.SSHUser,
		DestSSHUser:   cfg.Destination.SSHUser,
	})
}
