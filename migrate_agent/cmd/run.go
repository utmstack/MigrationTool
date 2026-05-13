package cmd

import (
	"context"
	"fmt"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/utmstack/MigrationTool/migration"
	svc "github.com/utmstack/MigrationTool/service"
	"github.com/utmstack/MigrationTool/utils"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the migration service",
	Long: `Runs the migration service loop.
	
When run interactively, the service runs in the foreground.
When run as a system service, it integrates with the service manager.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := utils.InitLogger(); err != nil {
			return fmt.Errorf("failed to initialize logger: %w", err)
		}

		utils.Logger.Info("Migration service starting")

		migrator, err := migration.NewMigrator()
		if err != nil {
			utils.Logger.ErrorF("Failed to create migrator: %v", err)
			return fmt.Errorf("failed to create migrator: %w", err)
		}

		runFunc := func(ctx context.Context) {
			if err := migrator.Run(ctx); err != nil {
				utils.Logger.ErrorF("Migrator exited with error: %v", err)
			}
		}

		prg := &svc.MigrationProgram{
			RunFunc: runFunc,
		}

		s, err := svc.NewService(prg)
		if err != nil {
			return fmt.Errorf("failed to create service: %w", err)
		}

		// Check if running interactively or as a service
		if service.Interactive() {
			utils.Logger.Info("Running in interactive mode")
			// Run directly without service wrapper
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			return migrator.Run(ctx)
		}

		// Run as a service
		utils.Logger.Info("Running as system service")
		return s.Run()
	},
}
