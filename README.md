# xstrapolate

<div align="center">

**A powerful CLI tool for creating lightweight Kubernetes clusters with Flux and GitOps-managed Crossplane**

[![Version](https://img.shields.io/badge/version-1.0.0-blue.svg)](https://github.com/drduker/xstrapolate/releases)
[![License](https://img.shields.io/badge/license-MIT-green.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/drduker/xstrapolate)](https://goreportcard.com/report/github.com/drduker/xstrapolate)

</div>

## 📋 Overview

xstrapolate creates lightweight Kubernetes clusters with Flux pre-installed and Crossplane managed via GitOps. Perfect for rapid development and testing with a secure, private-subnet approach.

## 🚀 Quick Start

```bash
# Clone and build
git clone https://github.com/drduker/xstrapolate.git
cd xstrapolate
go build -o xstrapolate .

# Initialize configuration
./xstrapolate init

# Create a fast single-node cluster (2-3 minutes)
./xstrapolate cluster create dev-cluster --cloud aws --type single-node

# Create production EKS cluster (10-15 minutes)
./xstrapolate cluster create prod-cluster --cloud aws --type eks
```

## ✨ Features

- ⚡ **Lightning Fast** - Single-node clusters ready in 2-3 minutes
- 🔒 **Secure by Default** - Private subnets, SSM access, no public IPs
- 🤖 **GitOps Native** - Flux installed, Crossplane via GitOps
- ☁️ **Multi-Cloud** - AWS (EKS/single-node) and Azure (AKS/single-node)
- 📝 **Config-Driven** - ~/.xstrapolate.yaml or CLI flags
- 🔧 **Auto-Setup** - Complete cluster setup with one command
- 🎯 **Smart AMI Lookup** - Automatically finds latest Amazon Linux 2023 AMI for any region

## 🔨 Building from Source

### Prerequisites
- Go 1.21 or later
- AWS CLI (for AWS clusters)
- Azure CLI (for Azure clusters)

### Build Instructions

```bash
# Clone the repository
git clone https://github.com/drduker/xstrapolate.git
cd xstrapolate

# Download dependencies and build
go mod download
go build -o xstrapolate .

# Optional: Install to PATH
sudo mv xstrapolate /usr/local/bin/
```

### Build for Different Platforms

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o xstrapolate-linux .

# macOS
GOOS=darwin GOARCH=amd64 go build -o xstrapolate-macos .

# Windows
GOOS=windows GOARCH=amd64 go build -o xstrapolate.exe .
```

## 📚 Usage Examples

### Initialize Configuration
```bash
# Create ~/.xstrapolate.yaml with default settings
./xstrapolate init
```

### AWS Single-Node Cluster (Fastest)
```bash
# Create cluster in private subnet with SSM access
# Automatically finds latest Amazon Linux 2023 AMI for the region

# Option 1: Use AWS_REGION environment variable (recommended)
export AWS_REGION=us-east-1
./xstrapolate cluster create my-dev --cloud aws --type single-node

# Option 2: Use --region flag
./xstrapolate cluster create my-dev --cloud aws --type single-node --region us-east-1

# Connect via SSM (no SSH keys needed)
aws ssm start-session --target <instance-id>

# Check setup progress (inside instance)
sudo journalctl -u cloud-final -f

# Verify cluster
sudo kubectl get nodes
sudo kubectl get pods -n flux-system
```

### AWS EKS Cluster (Production)
```bash
# Create managed EKS cluster
./xstrapolate cluster create my-prod --cloud aws --type eks --region us-west-2

# Configure kubectl
aws eks update-kubeconfig --region us-west-2 --name my-prod

# Verify cluster
kubectl get nodes
kubectl get pods -n flux-system
```

### Azure AKS Cluster
```bash
# Create AKS cluster
./xstrapolate cluster create my-aks --cloud azure --type aks

# Configure kubectl
az aks get-credentials --resource-group rg-my-aks --name my-aks

# Verify cluster
kubectl get nodes
```

### Cluster Deletion

⚠️ **Warning:** Cluster deletion is permanent and will remove ALL associated resources.

```bash
# Delete single-node cluster (removes EC2 instance, VPC, security groups, IAM roles)
./xstrapolate cluster teardown my-dev --cloud aws --force

# Delete EKS cluster
./xstrapolate cluster teardown my-prod --cloud aws --force

# Delete AKS cluster
./xstrapolate cluster teardown my-aks --cloud azure --force
```

**What gets deleted:**
- ✅ **EC2 instances** (for single-node clusters)
- ✅ **EKS clusters** (for managed clusters)
- ✅ **VPC endpoints** (SSM endpoints)
- ✅ **Subnets, security groups, route tables**
- ✅ **Internet gateways, NAT gateways**
- ✅ **VPCs** (created by xstrapolate)
- ✅ **IAM roles and instance profiles** (created by xstrapolate)

**Safety features:**
- 🛡️ **Requires `--force` flag** - prevents accidental deletion
- 🛡️ **Graceful cleanup** - proper deletion order to avoid dependency conflicts
- 🛡️ **Detailed logging** - shows exactly what's being removed
- 🛡️ **Continues on errors** - removes as much as possible even if some steps fail

## ⚙️ Configuration

The tool reads configuration from `~/.xstrapolate.yaml`:

```yaml
cloud:
  aws:
    region: "us-west-2"  # Default region (overridden by AWS_REGION env var or --region flag)
    # Optional: AWS credentials (uses AWS CLI/IAM roles by default)
    access_key_id: ""
    secret_access_key: ""

  azure:
    subscription_id: "your-subscription-id"
    tenant_id: "your-tenant-id"
    location: "eastus"
    # Optional: Service principal credentials
    client_id: ""
    client_secret: ""
```

### Region Configuration

AWS region is determined in the following order of precedence:

1. **Command line flag**: `--region us-east-1`
2. **Environment variable**: `export AWS_REGION=us-east-1`
3. **Config file**: `~/.xstrapolate.yaml`
4. **Default**: `us-west-2`

## 🔧 Commands

```bash
# Show all commands
./xstrapolate --help

# Show cluster commands
./xstrapolate cluster --help

# Create cluster with custom settings
./xstrapolate cluster create my-cluster \
  --cloud aws \
  --type single-node \
  --region us-east-1
```

## 🚨 Requirements

### AWS Setup
**Credentials Required:**
```bash
# Option 1: AWS CLI
aws configure

# Option 2: Environment variables
export AWS_ACCESS_KEY_ID=your-key-id
export AWS_SECRET_ACCESS_KEY=your-secret-key
export AWS_REGION=us-east-1

# Option 3: IAM roles (if running on EC2)
```

**Permissions Required:**
- EC2: Create instances, security groups, VPCs
- IAM: Create roles and instance profiles
- STS: Get caller identity
- SSM: Session Manager access
- EKS: Create and manage clusters (for EKS type)

### Azure Permissions
- Virtual Machines: Create and manage VMs
- AKS: Create and manage clusters (for AKS type)
- Resource Groups: Create and manage

## 📖 Documentation

For detailed documentation, see [USAGE.md](USAGE.md)

## 🤝 Contributing

Contributions are welcome! Please read our [Contributing Guide](CONTRIBUTING.md) for details.

## 📄 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.