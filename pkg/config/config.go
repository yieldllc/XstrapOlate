package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

type Config struct {
	Cloud CloudConfig `mapstructure:"cloud"`
}

type CloudConfig struct {
	AWS   AWSConfig   `mapstructure:"aws"`
	Azure AzureConfig `mapstructure:"azure"`
}

type AWSConfig struct {
	Region          string `mapstructure:"region"`
	AccessKeyID     string `mapstructure:"access_key_id"`
	SecretAccessKey string `mapstructure:"secret_access_key"`
	SessionToken    string `mapstructure:"session_token"`
}

type AzureConfig struct {
	SubscriptionID string `mapstructure:"subscription_id"`
	TenantID       string `mapstructure:"tenant_id"`
	ClientID       string `mapstructure:"client_id"`
	ClientSecret   string `mapstructure:"client_secret"`
	Location       string `mapstructure:"location"`
}

func Load() (*Config, error) {
	var cfg Config

	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return &cfg, nil
}

func CreateDefaultConfig() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	configPath := filepath.Join(home, ".xstrapolate.yaml")

	if _, err := os.Stat(configPath); err == nil {
		return fmt.Errorf("config file already exists at %s", configPath)
	}

	defaultConfig := `# XstrapOlate Configuration
# Copy this file to ~/.xstrapolate.yaml and fill in your credentials

cloud:
  aws:
    region: "us-west-2"
    # AWS credentials (optional if using AWS CLI/IAM roles)
    access_key_id: ""
    secret_access_key: ""
    session_token: ""

  azure:
    subscription_id: ""
    tenant_id: ""
    client_id: ""
    client_secret: ""
    location: "eastus"
`

	if err := os.WriteFile(configPath, []byte(defaultConfig), 0600); err != nil {
		return fmt.Errorf("failed to create config file: %w", err)
	}

	fmt.Printf("Default config created at %s\n", configPath)
	fmt.Println("Please edit the file and add your cloud credentials.")

	return nil
}