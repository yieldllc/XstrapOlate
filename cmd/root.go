package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string

var rootCmd = &cobra.Command{
	Use:   "xstrapolate",
	Short: "A CLI tool for creating lightweight Kubernetes clusters with Crossplane and Flux",
	Long: `xstrapolate is a CLI tool that simplifies the creation of lightweight
EKS or AKS clusters with Crossplane and Flux pre-installed.

It supports reading configuration from ~/.xstrapolate or using command-line flags.`,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version number",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("xstrapolate version %s\n", getVersion())
	},
}

func getVersion() string {
	// This will be replaced by the build system
	return "0.0.1"
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.AddCommand(versionCmd)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.xstrapolate.yaml)")
	rootCmd.PersistentFlags().String("cloud", "", "cloud provider (aws or azure)")

	viper.BindPFlag("cloud", rootCmd.PersistentFlags().Lookup("cloud"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".xstrapolate")
	}

	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	}
}