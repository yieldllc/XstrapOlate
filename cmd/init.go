package cmd

import (
	"github.com/drduker/xstrapolate/pkg/config"
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize xstrapolate configuration",
	Long:  `Create a default configuration file at ~/.xstrapolate.yaml`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return config.CreateDefaultConfig()
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
