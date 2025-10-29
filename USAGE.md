# XstrapOlate CLI Usage Guide

XstrapOlate is a CLI tool for quickly creating lightweight Kubernetes clusters with Crossplane and Flux pre-installed.

## Quick Start

### 1. Initialize Configuration

```bash
# Create default config file at ~/.xstrapolate.yaml
xstrapolate init
```

This creates a configuration file where you can set your cloud credentials and default settings.

### 2. Create a Cluster

```bash
# Create a single-node cluster on AWS (fastest - 2-3 minutes)
xstrapolate cluster create my-cluster --cloud aws --type single-node

# Create an EKS cluster on AWS (slower - 10-15 minutes)
xstrapolate cluster create my-cluster --cloud aws --type eks

# Create an AKS cluster on Azure
xstrapolate cluster create my-cluster --cloud azure --type aks
```

## Cluster Types

### Single-Node Clusters (Recommended for Development)
- **Fast**: Ready in 2-3 minutes
- **Lightweight**: Single EC2/VM instance with k3s in private subnet
- **Secure**: No public IP, access via AWS SSM
- **Cost-effective**: Minimal infrastructure
- **Auto-configured**: Crossplane, Flux, and kustomizations installed automatically
- **Perfect for**: Development, testing, learning

### Managed Clusters (EKS/AKS)
- **Production-ready**: Fully managed Kubernetes
- **Slower**: Takes 10-15 minutes to provision
- **More expensive**: Managed service costs
- **Perfect for**: Production workloads, team environments

## Configuration

### Config File Structure (~/.xstrapolate.yaml)

```yaml
cloud:
  aws:
    region: "us-west-2"
    access_key_id: ""      # Optional if using AWS CLI/IAM roles
    secret_access_key: ""  # Optional if using AWS CLI/IAM roles
    session_token: ""      # Optional

  azure:
    subscription_id: "your-subscription-id"
    tenant_id: "your-tenant-id"
    client_id: ""          # Optional if using Azure CLI
    client_secret: ""      # Optional if using Azure CLI
    location: "eastus"
```

### Command-line Overrides

```bash
# Override config file settings
xstrapolate cluster create my-cluster --cloud aws --region us-east-1 --type single-node
```

## Examples

### AWS Single-Node Development Cluster

```bash
# Create cluster (no public IP, uses SSM for access)
xstrapolate cluster create dev-cluster --cloud aws --type single-node

# Connect via AWS SSM (no SSH keys needed)
aws ssm start-session --target <instance-id>

# Check setup progress (inside the instance)
sudo journalctl -u cloud-final -f

# Get kubeconfig (inside the instance)
sudo cat /etc/rancher/k3s/k3s.yaml

# Verify everything is running
sudo kubectl get nodes
sudo kubectl get pods -n crossplane-system
sudo kubectl get pods -n flux-system
```

### AWS EKS Production Cluster

```bash
# Create EKS cluster
xstrapolate cluster create prod-cluster --cloud aws --type eks --region us-west-2

# Update kubeconfig (command provided in output)
aws eks update-kubeconfig --region us-west-2 --name prod-cluster

# Verify cluster
kubectl get nodes
kubectl get pods -n crossplane-system
kubectl get pods -n flux-system
```

### Azure AKS Cluster

```bash
# Create AKS cluster
xstrapolate cluster create my-aks --cloud azure --type aks

# Get credentials (command provided in output)
az aks get-credentials --resource-group rg-my-aks --name my-aks

# Verify cluster
kubectl get nodes
```

## After Cluster Creation

Once your cluster is created, you'll have:

1. **Crossplane** installed and ready for infrastructure provisioning
2. **Flux** installed and ready for GitOps workflows

### Setting up Flux with Git Repository

```bash
# Bootstrap Flux with your Git repository
flux bootstrap github \
  --owner=<github-username> \
  --repository=<repo-name> \
  --path=clusters/my-cluster \
  --token-auth

# Or with GitLab
flux bootstrap gitlab \
  --owner=<gitlab-username> \
  --repository=<repo-name> \
  --path=clusters/my-cluster \
  --token-auth
```

### Working with Crossplane

```bash
# Install a provider (example: AWS)
kubectl apply -f - <<EOF
apiVersion: pkg.crossplane.io/v1
kind: Provider
metadata:
  name: provider-aws
spec:
  package: xpkg.upbound.io/crossplane-contrib/provider-aws:v0.39.0
EOF

# Check provider status
kubectl get providers
```

## Prerequisites

- **Go 1.21+** (for building from source)
- **kubectl** (for cluster management)
- **helm** (for installing Crossplane)
- **flux** CLI (for GitOps setup)
- **AWS CLI** (for EKS clusters)
- **Azure CLI** (for AKS clusters)

## Cloud Requirements

### AWS
- Valid AWS credentials (CLI, IAM role, or config file)
- EC2 permissions for single-node clusters
- EKS permissions for managed clusters
- Default VPC and subnets

### Azure
- Valid Azure credentials (CLI or service principal)
- Resource group creation permissions
- AKS permissions for managed clusters

## Troubleshooting

### Build Issues
```bash
# If dependencies fail to download
go clean -modcache
go mod download
go build -o xstrapolate .
```

### AWS Permission Issues
```bash
# Test AWS credentials
aws sts get-caller-identity

# Check EKS permissions
aws eks list-clusters
```

### Azure Permission Issues
```bash
# Test Azure credentials
az account show

# Check AKS permissions
az aks list
```

## Performance Comparison

| Cluster Type | Provision Time | Cost | Use Case |
|-------------|----------------|------|----------|
| Single-node | 2-3 minutes | $20-40/month | Development, Testing |
| EKS | 10-15 minutes | $100+/month | Production, Teams |
| AKS | 10-15 minutes | $100+/month | Production, Teams |

For rapid development and testing, single-node clusters are the clear winner!