package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "migrate_agent",
	Short: "UTMStack Agent v10 to v11 Migration Service",
	Long: `A cross-platform service that monitors the UTMStack Agent 
and performs automatic migration from v10 to v11.

It polls the agent's version.json file and when v11 is detected,
it downloads the new binary, updates the service configuration,
and restarts the agent service.`,
}

// Execute adds all child commands to the root command and sets flags
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.AddCommand(installCmd)
	rootCmd.AddCommand(uninstallCmd)
	rootCmd.AddCommand(runCmd)
}
