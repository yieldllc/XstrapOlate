package k8s

import (
	"fmt"
	"os/exec"
	"strings"
)

func InstallCrossplane(kubeconfigPath string) error {
	fmt.Println("Installing Crossplane using Helm...")

	commands := [][]string{
		{"helm", "repo", "add", "crossplane-stable", "https://charts.crossplane.io/stable"},
		{"helm", "repo", "update"},
		{"kubectl", "--kubeconfig", kubeconfigPath, "create", "namespace", "crossplane-system", "--dry-run=client", "-o", "yaml"},
		{"kubectl", "--kubeconfig", kubeconfigPath, "apply", "-f", "-"},
		{"helm", "install", "crossplane", "crossplane-stable/crossplane",
			"--namespace", "crossplane-system",
			"--kubeconfig", kubeconfigPath,
			"--wait"},
	}

	for i, cmd := range commands {
		if i == 2 {
			// Create namespace if it doesn't exist
			createNsCmd := exec.Command(cmd[0], cmd[1:]...)
			output, err := createNsCmd.Output()
			if err != nil {
				fmt.Printf("Namespace might already exist: %v\n", err)
				continue
			}

			applyCmd := exec.Command("kubectl", "--kubeconfig", kubeconfigPath, "apply", "-f", "-")
			applyCmd.Stdin = strings.NewReader(string(output))
			if err := applyCmd.Run(); err != nil {
				fmt.Printf("Failed to create namespace: %v\n", err)
			}
			continue
		}

		execCmd := exec.Command(cmd[0], cmd[1:]...)
		fmt.Printf("Running: %s\n", execCmd.String())

		output, err := execCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to run command %v: %w\nOutput: %s", cmd, err, string(output))
		}

		fmt.Printf("✓ %s completed\n", cmd[0])
	}

	fmt.Println("✅ Crossplane installed successfully!")
	return nil
}