package cmd

import (
	"fmt"

	"github.com/drduker/xstrapolate/pkg/cloud"
	"github.com/drduker/xstrapolate/pkg/k8s"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var clusterCmd = &cobra.Command{
	Use:   "cluster",
	Short: "Manage Kubernetes clusters",
	Long:  `Create and manage Kubernetes clusters with Flux and GitOps-managed Crossplane`,
}

var createCmd = &cobra.Command{
	Use:   "create [cluster-name]",
	Short: "Create a new cluster",
	Long: `Create a new Kubernetes cluster with Flux installed and Crossplane via GitOps.

Supports:
- EKS clusters on AWS (--cloud aws --type eks)
- AKS clusters on Azure (--cloud azure --type aks)
- Single node clusters (--type single-node) - fastest option, private subnet + SSM access`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		cloudProvider := viper.GetString("cloud")
		clusterType := viper.GetString("type")

		if cloudProvider == "" {
			return fmt.Errorf("cloud provider must be specified (--cloud aws or --cloud azure)")
		}

		fmt.Printf("Creating %s cluster '%s' on %s...\n", clusterType, clusterName, cloudProvider)

		var manager cloud.ClusterManager
		var err error

		switch cloudProvider {
		case "aws":
			manager, err = cloud.NewAWSManager()
		case "azure":
			manager, err = cloud.NewAzureManager()
		default:
			return fmt.Errorf("unsupported cloud provider: %s", cloudProvider)
		}

		if err != nil {
			return fmt.Errorf("failed to initialize cloud manager: %w", err)
		}

		cluster, err := manager.CreateCluster(clusterName, clusterType)
		if err != nil {
			return fmt.Errorf("failed to create cluster: %w", err)
		}

		fmt.Printf("Cluster '%s' created successfully!\n", cluster.Name)
		fmt.Printf("Kubeconfig: %s\n", cluster.KubeconfigPath)

		// For single-node clusters, Flux is installed via user data
		if clusterType == "single-node" {
			fmt.Println("✅ Cluster provisioning started!")
			fmt.Println("Flux will be installed automatically during startup.")
			fmt.Println("Crossplane will be installed via Flux GitOps from the official repo.")
		} else {
			// For managed clusters (EKS/AKS), install manually
			fmt.Println("Installing Flux...")
			if err := k8s.InstallFlux(cluster.KubeconfigPath); err != nil {
				return fmt.Errorf("failed to install Flux: %w", err)
			}

			fmt.Println("✅ Cluster setup complete!")
			fmt.Println("💡 Install Crossplane via Flux by applying your GitOps configuration.")
		}
		return nil
	},
}

var teardownCmd = &cobra.Command{
	Use:   "teardown [cluster-name]",
	Short: "Teardown a cluster and all associated resources",
	Long: `Teardown a Kubernetes cluster and clean up all associated cloud resources.

This will delete:
- EC2 instances
- VPC endpoints
- Subnets
- Security groups
- Internet gateways
- Route tables
- VPCs
- IAM roles and instance profiles (created by xstrapolate)

WARNING: This action is irreversible and will delete all data in the cluster.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		clusterName := args[0]
		cloudProvider := viper.GetString("cloud")
		force := viper.GetBool("force")

		if cloudProvider == "" {
			return fmt.Errorf("cloud provider must be specified (--cloud aws or --cloud azure)")
		}

		if !force {
			fmt.Printf("⚠️  WARNING: This will permanently delete cluster '%s' and ALL associated resources!\n", clusterName)
			fmt.Println("Use --force flag to confirm deletion")
			return fmt.Errorf("teardown cancelled - use --force to confirm")
		}

		fmt.Printf("🗑️  Tearing down %s cluster '%s'...\n", cloudProvider, clusterName)

		var manager cloud.ClusterManager
		var err error

		switch cloudProvider {
		case "aws":
			manager, err = cloud.NewAWSManager()
		case "azure":
			manager, err = cloud.NewAzureManager()
		default:
			return fmt.Errorf("unsupported cloud provider: %s", cloudProvider)
		}

		if err != nil {
			return fmt.Errorf("failed to initialize cloud manager: %w", err)
		}

		err = manager.DeleteCluster(clusterName)
		if err != nil {
			return fmt.Errorf("failed to teardown cluster: %w", err)
		}

		fmt.Printf("✅ Cluster '%s' and all resources successfully deleted!\n", clusterName)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(clusterCmd)
	clusterCmd.AddCommand(createCmd)
	clusterCmd.AddCommand(teardownCmd)

	createCmd.Flags().String("type", "single-node", "cluster type (eks, aks, single-node)")
	createCmd.Flags().String("region", "", "cloud region")
	createCmd.Flags().String("node-count", "1", "number of nodes")

	teardownCmd.Flags().Bool("force", false, "force teardown without confirmation")

	viper.BindPFlag("type", createCmd.Flags().Lookup("type"))
	viper.BindPFlag("region", createCmd.Flags().Lookup("region"))
	viper.BindPFlag("node-count", createCmd.Flags().Lookup("node-count"))
	viper.BindPFlag("force", teardownCmd.Flags().Lookup("force"))
}
