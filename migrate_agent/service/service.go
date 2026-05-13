package service

import (
	"context"

	"github.com/kardianos/service"
	"github.com/utmstack/MigrationTool/config"
	"github.com/utmstack/MigrationTool/utils"
)

// MigrationProgram implements the kardianos/service.Interface
type MigrationProgram struct {
	RunFunc func(ctx context.Context)
	ctx     context.Context
	cancel  context.CancelFunc
}

// Start is called when the service is started
func (p *MigrationProgram) Start(s service.Service) error {
	if utils.Logger != nil {
		utils.Logger.Info("Migration service starting")
	}

	p.ctx, p.cancel = context.WithCancel(context.Background())
	go p.run()
	return nil
}

// Stop is called when the service is stopped
func (p *MigrationProgram) Stop(s service.Service) error {
	if utils.Logger != nil {
		utils.Logger.Info("Migration service stopping")
	}

	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

func (p *MigrationProgram) run() {
	if p.RunFunc != nil {
		p.RunFunc(p.ctx)
	}
}

// NewService creates a new kardianos service instance
func NewService(program *MigrationProgram) (service.Service, error) {
	svcConfig := &service.Config{
		Name:        config.MigrationServiceName,
		DisplayName: config.MigrationServiceDisplayName,
		Description: config.MigrationServiceDescription,
		Arguments:   []string{"run"},
	}

	return service.New(program, svcConfig)
}
