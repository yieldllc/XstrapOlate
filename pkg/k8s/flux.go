package k8s

import (
	"fmt"
	"os/exec"
)

func InstallFlux(kubeconfigPath string) error {
	fmt.Println("Installing Flux...")

	commands := [][]string{
		{"flux", "check", "--pre"},
		{"flux", "install", "--kubeconfig", kubeconfigPath},
	}

	for _, cmd := range commands {
		execCmd := exec.Command(cmd[0], cmd[1:]...)
		fmt.Printf("Running: %s\n", execCmd.String())

		output, err := execCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to run command %v: %w\nOutput: %s", cmd, err, string(output))
		}

		fmt.Printf("✓ %s completed\n", cmd[0])
	}

	fmt.Println("✅ Flux installed successfully!")
	fmt.Println("💡 To bootstrap a Git repository, run:")
	fmt.Println("   flux bootstrap github --owner=<user> --repository=<repo> --path=clusters/my-cluster")

	return nil
}