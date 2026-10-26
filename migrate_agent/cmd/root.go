package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "migrate_agent",
	Short: "UTMStack Agent v11 to v12 Migration Service",
	Long: `A cross-platform service that performs the local part of the
UTMStack Agent v11 to v12 migration.

It polls the panel's version.json over HTTPS and, once the panel
reports a V12 and this agent's id is listed there, it uninstalls
the local V11 agent, downloads the V12 binary from the panel,
installs it fresh with the panel connection key, verifies the
result and removes itself.`,
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
