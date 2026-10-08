package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "utmstack_migration_tool",
	Short: "UTMStack Migration Tool — V11 to V12",
	Long: `UTMStack Migration Tool — V11 to V12

Migrate your UTMStack data safely and automatically.

Commands:
  prepare-agents   Pre-deploy migrate_agent to every UTMStack agent (run BEFORE upgrade)
  upgrade          In-place upgrade on a single instance (destructive)
  migrate          Full migration between two instances (export + import)
  export           Export V11 data to file
  import           Import data file into V12

Recommended workflow:
  1. utmstack_migration_tool prepare-agents --config config.yaml
  2. utmstack_migration_tool upgrade [--config config.yaml]

Use "utmstack_migration_tool <command> --help" for command-specific options.`,
	Version:       "1.0.0",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(prepareAgentsCmd)
	rootCmd.AddCommand(migrateCmd)
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(upgradeCmd)
}
