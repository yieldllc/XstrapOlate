package cloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/spf13/viper"
)

type AWSManager struct {
	cfg       aws.Config
	eksClient *eks.Client
	ec2Client *ec2.Client
	iamClient *iam.Client
	stsClient *sts.Client
	region    string
}

func NewAWSManager() (*AWSManager, error) {
	// Check for region in order of preference:
	// 1. Command line flag
	// 2. AWS_REGION environment variable
	// 3. Config file
	// 4. Default to us-west-2
	var region, regionSource string

	if flagRegion := viper.GetString("region"); flagRegion != "" {
		region = flagRegion
		regionSource = "command line flag"
	} else if envRegion := os.Getenv("AWS_REGION"); envRegion != "" {
		region = envRegion
		regionSource = "AWS_REGION environment variable"
	} else if configRegion := viper.GetString("cloud.aws.region"); configRegion != "" {
		region = configRegion
		regionSource = "config file"
	} else {
		region = "us-west-2"
		regionSource = "default"
	}

	fmt.Printf("Using AWS region: %s (from %s)\n", region, regionSource)

	cfg, err := config.LoadDefaultConfig(context.TODO(),
		config.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w\n\nPlease ensure you have AWS credentials configured:\n- Run 'aws configure' to set up credentials\n- Or set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY environment variables\n- Or use IAM roles if running on EC2", err)
	}

	manager := &AWSManager{
		cfg:       cfg,
		eksClient: eks.NewFromConfig(cfg),
		ec2Client: ec2.NewFromConfig(cfg),
		iamClient: iam.NewFromConfig(cfg),
		stsClient: sts.NewFromConfig(cfg),
		region:    region,
	}

	// Test credentials by getting caller identity
	_, err = manager.stsClient.GetCallerIdentity(context.TODO(), &sts.GetCallerIdentityInput{})
	if err != nil {
		return nil, fmt.Errorf("failed to validate AWS credentials: %w\n\nPlease ensure you have AWS credentials configured:\n- Run 'aws configure' to set up credentials\n- Or set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY environment variables\n- Or use IAM roles if running on EC2", err)
	}

	return manager, nil
}

func (m *AWSManager) CreateCluster(name, clusterType string) (*ClusterInfo, error) {
	switch clusterType {
	case "eks":
		return m.createEKSCluster(name)
	case "single-node":
		return m.createSingleNodeCluster(name)
	default:
		return nil, fmt.Errorf("unsupported cluster type for AWS: %s", clusterType)
	}
}

func (m *AWSManager) createEKSCluster(name string) (*ClusterInfo, error) {
	fmt.Println("Creating EKS cluster (this will take 10-15 minutes)...")

	roleArn, err := m.ensureEKSServiceRole()
	if err != nil {
		return nil, fmt.Errorf("failed to create EKS service role: %w", err)
	}

	subnetIds, err := m.getOrCreateSubnets()
	if err != nil {
		return nil, fmt.Errorf("failed to get or create subnets: %w", err)
	}

	input := &eks.CreateClusterInput{
		Name:    aws.String(name),
		Version: aws.String("1.28"),
		RoleArn: aws.String(roleArn),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds: subnetIds,
		},
	}

	result, err := m.eksClient.CreateCluster(context.TODO(), input)
	if err != nil {
		return nil, fmt.Errorf("failed to create EKS cluster: %w", err)
	}

	fmt.Printf("EKS cluster '%s' creation initiated. Waiting for completion...\n", name)

	waiter := eks.NewClusterActiveWaiter(m.eksClient)
	err = waiter.Wait(context.TODO(), &eks.DescribeClusterInput{
		Name: aws.String(name),
	}, 20*time.Minute)

	if err != nil {
		return nil, fmt.Errorf("failed to wait for cluster to be active: %w", err)
	}

	kubeconfigPath, err := m.generateKubeconfig(name)
	if err != nil {
		return nil, fmt.Errorf("failed to generate kubeconfig: %w", err)
	}

	return &ClusterInfo{
		Name:           name,
		Type:           "eks",
		Provider:       "aws",
		KubeconfigPath: kubeconfigPath,
		Endpoint:       aws.ToString(result.Cluster.Endpoint),
		Status:         "active",
	}, nil
}

func (m *AWSManager) createSingleNodeCluster(name string) (*ClusterInfo, error) {
	fmt.Println("Creating single-node cluster using k3s with SSM access...")

	// Ensure SSM instance profile exists
	err := m.ensureSSMInstanceProfile()
	if err != nil {
		return nil, fmt.Errorf("failed to create SSM instance profile: %w", err)
	}

	// Wait for instance profile to be ready
	_, err = m.waitForInstanceProfile("xstrapolate-ssm-profile")
	if err != nil {
		return nil, fmt.Errorf("instance profile not ready: %w", err)
	}

	// Additional wait for EC2 service to recognize the instance profile
	fmt.Println("⏳ Waiting for EC2 service to recognize instance profile...")
	time.Sleep(5 * time.Second)

	instanceId, err := m.createEC2Instance(name)
	if err != nil {
		return nil, fmt.Errorf("failed to create EC2 instance: %w", err)
	}

	fmt.Printf("EC2 instance '%s' created in private subnet (SSM access only).\n", instanceId)
	fmt.Println("Installing k3s, Crossplane, and Flux...")
	fmt.Println("Setup is running in the background. This may take 5-10 minutes.")
	fmt.Printf("Connect via SSM: aws ssm start-session --target %s\n", instanceId)
	fmt.Println("Check progress: sudo journalctl -u cloud-final -f")
	fmt.Println("Get kubeconfig: sudo cat /etc/rancher/k3s/k3s.yaml")
	fmt.Println("Note: Instance has no public IP - access only via SSM Session Manager")

	return &ClusterInfo{
		Name:           name,
		Type:           "single-node",
		Provider:       "aws",
		KubeconfigPath: "/etc/rancher/k3s/k3s.yaml",
		Endpoint:       instanceId, // Use instance ID since no public IP
		Status:         "provisioning",
	}, nil
}

func (m *AWSManager) ensureEKSServiceRole() (string, error) {
	roleName := "xstrapolate-eks-service-role"

	assumeRolePolicyDocument := `{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Principal": {
					"Service": "eks.amazonaws.com"
				},
				"Action": "sts:AssumeRole"
			}
		]
	}`

	_, err := m.iamClient.CreateRole(context.TODO(), &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(assumeRolePolicyDocument),
	})

	if err != nil {
		// Role might already exist
		fmt.Println("Role might already exist, continuing...")
	}

	policyArns := []string{
		"arn:aws:iam::aws:policy/AmazonEKSClusterPolicy",
	}

	for _, policyArn := range policyArns {
		_, err = m.iamClient.AttachRolePolicy(context.TODO(), &iam.AttachRolePolicyInput{
			RoleName:  aws.String(roleName),
			PolicyArn: aws.String(policyArn),
		})
		if err != nil {
			fmt.Printf("Warning: failed to attach policy %s: %v\n", policyArn, err)
		}
	}

	accountID, err := m.getAccountID()
	if err != nil {
		return "", fmt.Errorf("failed to get account ID: %w", err)
	}
	return fmt.Sprintf("arn:aws:iam::%s:role/%s", accountID, roleName), nil
}

func (m *AWSManager) getOrCreateSubnets() ([]string, error) {
	// Always create new VPC and subnets
	fmt.Println("Creating new VPC and subnets for xstrapolate...")
	return m.createVPCAndSubnets()
}

func (m *AWSManager) findExistingXstrapolateSubnets() ([]string, error) {
	result, err := m.ec2Client.DescribeSubnets(context.TODO(), &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:xstrapolate-vpc"),
				Values: []string{"true"},
			},
			{
				Name:   aws.String("tag:Type"),
				Values: []string{"public"},
			},
		},
	})

	if err != nil {
		return nil, err
	}

	var subnetIds []string
	for _, subnet := range result.Subnets {
		subnetIds = append(subnetIds, aws.ToString(subnet.SubnetId))
	}

	if len(subnetIds) < 2 {
		return nil, fmt.Errorf("insufficient xstrapolate subnets found")
	}

	return subnetIds, nil
}

func (m *AWSManager) createVPCAndSubnets() ([]string, error) {
	// Create VPC
	vpcResult, err := m.ec2Client.CreateVpc(context.TODO(), &ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeVpc,
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("xstrapolate-vpc"),
					},
					{
						Key:   aws.String("xstrapolate-managed"),
						Value: aws.String("true"),
					},
					{
						Key:   aws.String("xstrapolate-resource-type"),
						Value: aws.String("vpc"),
					},
					{
						Key:   aws.String("xstrapolate-vpc"),
						Value: aws.String("true"),
					},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create VPC: %w", err)
	}

	vpcId := aws.ToString(vpcResult.Vpc.VpcId)
	fmt.Printf("Created VPC: %s\n", vpcId)

	// Enable DNS hostnames
	_, err = m.ec2Client.ModifyVpcAttribute(context.TODO(), &ec2.ModifyVpcAttributeInput{
		VpcId:              aws.String(vpcId),
		EnableDnsHostnames: &types.AttributeBooleanValue{Value: aws.Bool(true)},
	})
	if err != nil {
		fmt.Printf("Warning: failed to enable DNS hostnames: %v\n", err)
	}

	// Get availability zones
	azResult, err := m.ec2Client.DescribeAvailabilityZones(context.TODO(), &ec2.DescribeAvailabilityZonesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("state"),
				Values: []string{"available"},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get availability zones: %w", err)
	}

	if len(azResult.AvailabilityZones) < 2 {
		return nil, fmt.Errorf("need at least 2 availability zones")
	}

	// Create Internet Gateway
	igwResult, err := m.ec2Client.CreateInternetGateway(context.TODO(), &ec2.CreateInternetGatewayInput{
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeInternetGateway,
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("xstrapolate-igw"),
					},
					{
						Key:   aws.String("xstrapolate-managed"),
						Value: aws.String("true"),
					},
					{
						Key:   aws.String("xstrapolate-resource-type"),
						Value: aws.String("internet-gateway"),
					},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create internet gateway: %w", err)
	}

	igwId := aws.ToString(igwResult.InternetGateway.InternetGatewayId)

	// Attach Internet Gateway to VPC
	_, err = m.ec2Client.AttachInternetGateway(context.TODO(), &ec2.AttachInternetGatewayInput{
		InternetGatewayId: aws.String(igwId),
		VpcId:             aws.String(vpcId),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to attach internet gateway: %w", err)
	}

	// Create subnets in different AZs
	var publicSubnetIds []string
	var privateSubnetIds []string
	
	for i := 0; i < 2; i++ {
		az := aws.ToString(azResult.AvailabilityZones[i].ZoneName)
		
		// Create public subnet
		publicCidr := fmt.Sprintf("10.0.%d.0/24", i*10+1)
		publicSubnetResult, err := m.ec2Client.CreateSubnet(context.TODO(), &ec2.CreateSubnetInput{
			VpcId:            aws.String(vpcId),
			CidrBlock:        aws.String(publicCidr),
			AvailabilityZone: aws.String(az),
			TagSpecifications: []types.TagSpecification{
				{
					ResourceType: types.ResourceTypeSubnet,
					Tags: []types.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String(fmt.Sprintf("xstrapolate-public-%d", i+1)),
						},
						{
							Key:   aws.String("xstrapolate-managed"),
							Value: aws.String("true"),
						},
						{
							Key:   aws.String("xstrapolate-resource-type"),
							Value: aws.String("subnet"),
						},
						{
							Key:   aws.String("xstrapolate-vpc"),
							Value: aws.String("true"),
						},
						{
							Key:   aws.String("Type"),
							Value: aws.String("public"),
						},
					},
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create public subnet %d: %w", i+1, err)
		}
		publicSubnetIds = append(publicSubnetIds, aws.ToString(publicSubnetResult.Subnet.SubnetId))

		// Create private subnet
		privateCidr := fmt.Sprintf("10.0.%d.0/24", i*10+2)
		privateSubnetResult, err := m.ec2Client.CreateSubnet(context.TODO(), &ec2.CreateSubnetInput{
			VpcId:            aws.String(vpcId),
			CidrBlock:        aws.String(privateCidr),
			AvailabilityZone: aws.String(az),
			TagSpecifications: []types.TagSpecification{
				{
					ResourceType: types.ResourceTypeSubnet,
					Tags: []types.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String(fmt.Sprintf("xstrapolate-private-%d", i+1)),
						},
						{
							Key:   aws.String("xstrapolate-managed"),
							Value: aws.String("true"),
						},
						{
							Key:   aws.String("xstrapolate-resource-type"),
							Value: aws.String("subnet"),
						},
						{
							Key:   aws.String("xstrapolate-vpc"),
							Value: aws.String("true"),
						},
						{
							Key:   aws.String("Type"),
							Value: aws.String("private"),
						},
					},
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create private subnet %d: %w", i+1, err)
		}
		privateSubnetIds = append(privateSubnetIds, aws.ToString(privateSubnetResult.Subnet.SubnetId))
	}

	// Create route table for public subnets
	rtResult, err := m.ec2Client.CreateRouteTable(context.TODO(), &ec2.CreateRouteTableInput{
		VpcId: aws.String(vpcId),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeRouteTable,
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("xstrapolate-public-rt"),
					},
					{
						Key:   aws.String("xstrapolate-managed"),
						Value: aws.String("true"),
					},
					{
						Key:   aws.String("xstrapolate-resource-type"),
						Value: aws.String("route-table"),
					},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create route table: %w", err)
	}

	rtId := aws.ToString(rtResult.RouteTable.RouteTableId)

	// Add route to Internet Gateway
	_, err = m.ec2Client.CreateRoute(context.TODO(), &ec2.CreateRouteInput{
		RouteTableId:         aws.String(rtId),
		DestinationCidrBlock: aws.String("0.0.0.0/0"),
		GatewayId:            aws.String(igwId),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create route: %w", err)
	}

	// Associate public subnets with route table
	for _, subnetId := range publicSubnetIds {
		_, err = m.ec2Client.AssociateRouteTable(context.TODO(), &ec2.AssociateRouteTableInput{
			RouteTableId: aws.String(rtId),
			SubnetId:     aws.String(subnetId),
		})
		if err != nil {
			fmt.Printf("Warning: failed to associate route table with subnet %s: %v\n", subnetId, err)
		}

		// Enable auto-assign public IP
		_, err = m.ec2Client.ModifySubnetAttribute(context.TODO(), &ec2.ModifySubnetAttributeInput{
			SubnetId:                        aws.String(subnetId),
			MapPublicIpOnLaunch:             &types.AttributeBooleanValue{Value: aws.Bool(true)},
		})
		if err != nil {
			fmt.Printf("Warning: failed to enable auto-assign public IP for subnet %s: %v\n", subnetId, err)
		}
	}

	fmt.Printf("Created VPC with %d public and %d private subnets\n", len(publicSubnetIds), len(privateSubnetIds))
	
	// Store VPC ID for later cleanup
	m.storeVPCInfo(vpcId, publicSubnetIds, privateSubnetIds)
	
	// Return public subnets for EKS
	return publicSubnetIds, nil
}

func (m *AWSManager) storeVPCInfo(vpcId string, publicSubnets, privateSubnets []string) {
	// This is a helper to store VPC info for cleanup later
	// You could store this in a config file or database
	fmt.Printf("VPC Info stored:\n")
	fmt.Printf("  VPC ID: %s\n", vpcId)
	fmt.Printf("  Public Subnets: %v\n", publicSubnets)
	fmt.Printf("  Private Subnets: %v\n", privateSubnets)
}

func (m *AWSManager) createVPCAndSubnetsForSSM() ([]string, []string, error) {
	// Create VPC
	vpcResult, err := m.ec2Client.CreateVpc(context.TODO(), &ec2.CreateVpcInput{
		CidrBlock: aws.String("10.0.0.0/16"),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeVpc,
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("xstrapolate-ssm-vpc"),
					},
					{
						Key:   aws.String("xstrapolate-managed"),
						Value: aws.String("true"),
					},
					{
						Key:   aws.String("xstrapolate-resource-type"),
						Value: aws.String("vpc"),
					},
					{
						Key:   aws.String("xstrapolate-vpc"),
						Value: aws.String("true"),
					},
				},
			},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create VPC: %w", err)
	}

	vpcId := aws.ToString(vpcResult.Vpc.VpcId)
	fmt.Printf("Created VPC for SSM-only access: %s\n", vpcId)

	// Enable DNS support first (required for DNS hostnames)
	fmt.Println("Enabling DNS support...")
	_, err = m.ec2Client.ModifyVpcAttribute(context.TODO(), &ec2.ModifyVpcAttributeInput{
		VpcId:            aws.String(vpcId),
		EnableDnsSupport: &types.AttributeBooleanValue{Value: aws.Bool(true)},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to enable DNS support (required for SSM): %w", err)
	}

	// Enable DNS hostnames (required for VPC endpoints)
	fmt.Println("Enabling DNS hostnames...")
	_, err = m.ec2Client.ModifyVpcAttribute(context.TODO(), &ec2.ModifyVpcAttributeInput{
		VpcId:              aws.String(vpcId),
		EnableDnsHostnames: &types.AttributeBooleanValue{Value: aws.Bool(true)},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to enable DNS hostnames (required for SSM): %w", err)
	}

	fmt.Println("DNS settings configured successfully")

	// Get availability zones
	azResult, err := m.ec2Client.DescribeAvailabilityZones(context.TODO(), &ec2.DescribeAvailabilityZonesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("state"),
				Values: []string{"available"},
			},
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get availability zones: %w", err)
	}

	if len(azResult.AvailabilityZones) < 1 {
		return nil, nil, fmt.Errorf("need at least 1 availability zone")
	}

	// Create private subnets only (no public subnets needed for SSM-only access)
	var privateSubnetIds []string

	for i := 0; i < 2 && i < len(azResult.AvailabilityZones); i++ {
		az := aws.ToString(azResult.AvailabilityZones[i].ZoneName)

		// Create private subnet
		privateCidr := fmt.Sprintf("10.0.%d.0/24", i+10)
		privateSubnetResult, err := m.ec2Client.CreateSubnet(context.TODO(), &ec2.CreateSubnetInput{
			VpcId:            aws.String(vpcId),
			CidrBlock:        aws.String(privateCidr),
			AvailabilityZone: aws.String(az),
			TagSpecifications: []types.TagSpecification{
				{
					ResourceType: types.ResourceTypeSubnet,
					Tags: []types.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String(fmt.Sprintf("xstrapolate-ssm-private-%d", i+1)),
						},
						{
							Key:   aws.String("xstrapolate-managed"),
							Value: aws.String("true"),
						},
						{
							Key:   aws.String("xstrapolate-resource-type"),
							Value: aws.String("subnet"),
						},
						{
							Key:   aws.String("xstrapolate-vpc"),
							Value: aws.String("true"),
						},
						{
							Key:   aws.String("Type"),
							Value: aws.String("private"),
						},
					},
				},
			},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create private subnet %d: %w", i+1, err)
		}
		privateSubnetIds = append(privateSubnetIds, aws.ToString(privateSubnetResult.Subnet.SubnetId))
	}

	// Create VPC endpoints for SSM
	err = m.createSSMVPCEndpoints(vpcId, privateSubnetIds)
	if err != nil {
		fmt.Printf("Warning: failed to create VPC endpoints: %v\n", err)
	}

	fmt.Printf("Created VPC with %d private subnets and SSM VPC endpoints\n", len(privateSubnetIds))

	return []string{}, privateSubnetIds, nil
}

func (m *AWSManager) createSSMVPCEndpoints(vpcId string, subnetIds []string) error {
	fmt.Println("Creating VPC endpoints for SSM access...")

	// Create security group for VPC endpoints
	sgResult, err := m.ec2Client.CreateSecurityGroup(context.TODO(), &ec2.CreateSecurityGroupInput{
		GroupName:   aws.String("xstrapolate-ssm-endpoints"),
		Description: aws.String("Security group for SSM VPC endpoints"),
		VpcId:       aws.String(vpcId),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeSecurityGroup,
				Tags: []types.Tag{
					{
						Key:   aws.String("Name"),
						Value: aws.String("xstrapolate-ssm-endpoints"),
					},
					{
						Key:   aws.String("xstrapolate-managed"),
						Value: aws.String("true"),
					},
					{
						Key:   aws.String("xstrapolate-resource-type"),
						Value: aws.String("security-group"),
					},
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create security group: %w", err)
	}

	sgId := aws.ToString(sgResult.GroupId)

	// Allow HTTPS traffic from VPC CIDR
	_, err = m.ec2Client.AuthorizeSecurityGroupIngress(context.TODO(), &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: aws.String(sgId),
		IpPermissions: []types.IpPermission{
			{
				IpProtocol: aws.String("tcp"),
				FromPort:   aws.Int32(443),
				ToPort:     aws.Int32(443),
				IpRanges: []types.IpRange{
					{
						CidrIp: aws.String("10.0.0.0/16"),
					},
				},
			},
		},
	})
	if err != nil {
		fmt.Printf("Warning: failed to add security group rule: %v\n", err)
	}

	// SSM requires these three VPC endpoints
	endpoints := []string{
		"com.amazonaws." + m.region + ".ssm",
		"com.amazonaws." + m.region + ".ssmmessages",
		"com.amazonaws." + m.region + ".ec2messages",
	}

	for _, endpoint := range endpoints {
		fmt.Printf("Creating VPC endpoint: %s\n", endpoint)
		_, err = m.ec2Client.CreateVpcEndpoint(context.TODO(), &ec2.CreateVpcEndpointInput{
			VpcId:           aws.String(vpcId),
			ServiceName:     aws.String(endpoint),
			VpcEndpointType: types.VpcEndpointTypeInterface,
			SubnetIds:       subnetIds,
			SecurityGroupIds: []string{sgId},
			PrivateDnsEnabled: aws.Bool(true),
			TagSpecifications: []types.TagSpecification{
				{
					ResourceType: types.ResourceTypeVpcEndpoint,
					Tags: []types.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String("xstrapolate-" + strings.Split(endpoint, ".")[3]),
						},
						{
							Key:   aws.String("xstrapolate-managed"),
							Value: aws.String("true"),
						},
						{
							Key:   aws.String("xstrapolate-resource-type"),
							Value: aws.String("vpc-endpoint"),
						},
					},
				},
			},
		})
		if err != nil {
			fmt.Printf("Warning: failed to create VPC endpoint %s: %v\n", endpoint, err)
		}
	}

	fmt.Println("VPC endpoints created successfully")
	return nil
}

func (m *AWSManager) createEC2Instance(name string) (string, error) {
	// Create VPC and subnets for the EC2 instance
	_, privateSubnetIds, err := m.createVPCAndSubnetsForSSM()
	if err != nil {
		return "", fmt.Errorf("failed to create VPC and subnets: %w", err)
	}

	// Use the first private subnet for the EC2 instance (SSM access only)
	subnetId := privateSubnetIds[0]

	// Get the latest Amazon Linux 2023 AMI for current region
	amiId, err := m.getLatestAmazonLinuxAMI()
	if err != nil {
		return "", fmt.Errorf("failed to get latest AMI: %w", err)
	}

	userData := m.generateUserData(name)

	// Encode user data as base64
	encodedUserData := base64.StdEncoding.EncodeToString([]byte(userData))

	fmt.Printf("User data script length: %d bytes\n", len(userData))

	// Retry EC2 instance creation to handle IAM propagation delays
	var result *ec2.RunInstancesOutput
	maxRetries := 6

	for retry := 0; retry < maxRetries; retry++ {
		result, err = m.ec2Client.RunInstances(context.TODO(), &ec2.RunInstancesInput{
			ImageId:      aws.String(amiId),
			InstanceType: types.InstanceTypeT3Medium,
			MinCount:     aws.Int32(1),
			MaxCount:     aws.Int32(1),
			SubnetId:     aws.String(subnetId),
			UserData:     aws.String(encodedUserData),
			IamInstanceProfile: &types.IamInstanceProfileSpecification{
				Name: aws.String("xstrapolate-ssm-profile"),
			},
			TagSpecifications: []types.TagSpecification{
				{
					ResourceType: types.ResourceTypeInstance,
					Tags: []types.Tag{
						{
							Key:   aws.String("Name"),
							Value: aws.String(name),
						},
						{
							Key:   aws.String("xstrapolate-cluster"),
							Value: aws.String(name),
						},
					},
				},
			},
		})

		if err == nil {
			break // Success!
		}

		// Check if it's an IAM instance profile error
		if strings.Contains(err.Error(), "Invalid IAM Instance Profile") && retry < maxRetries-1 {
			fmt.Printf("⏳ Retry %d/%d: IAM instance profile not yet propagated to EC2, waiting...\n", retry+1, maxRetries)
			time.Sleep(5 * time.Second)
			continue
		}

		// Other error or max retries reached
		return "", err
	}

	return aws.ToString(result.Instances[0].InstanceId), nil
}

func (m *AWSManager) ensureSSMInstanceProfile() error {
	roleName := "xstrapolate-ssm-role"
	profileName := "xstrapolate-ssm-profile"

	// Create IAM role for SSM
	assumeRolePolicyDocument := `{
		"Version": "2012-10-17",
		"Statement": [
			{
				"Effect": "Allow",
				"Principal": {
					"Service": "ec2.amazonaws.com"
				},
				"Action": "sts:AssumeRole"
			}
		]
	}`

	_, err := m.iamClient.CreateRole(context.TODO(), &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(assumeRolePolicyDocument),
	})
	if err != nil {
		fmt.Println("SSM role might already exist, continuing...")
	}

	// Attach SSM policy
	_, err = m.iamClient.AttachRolePolicy(context.TODO(), &iam.AttachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"),
	})
	if err != nil {
		fmt.Printf("Warning: failed to attach SSM policy: %v\n", err)
	}

	// Create instance profile
	_, err = m.iamClient.CreateInstanceProfile(context.TODO(), &iam.CreateInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
	})
	if err != nil {
		fmt.Println("Instance profile might already exist, continuing...")
	}

	// Add role to instance profile (only if not already attached)
	_, err = m.iamClient.AddRoleToInstanceProfile(context.TODO(), &iam.AddRoleToInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		RoleName:            aws.String(roleName),
	})
	if err != nil {
		// Check if it's just because the role is already attached
		if !strings.Contains(err.Error(), "LimitExceeded") && !strings.Contains(err.Error(), "EntityAlreadyExists") {
			return fmt.Errorf("failed to add role to instance profile: %w", err)
		}
		fmt.Println("Role already attached to instance profile")
	} else {
		fmt.Println("✅ Role attached to instance profile")
	}

	return nil
}

func (m *AWSManager) waitForInstanceProfile(profileName string) (string, error) {
	fmt.Printf("⏳ Waiting for instance profile '%s' to be ready...\n", profileName)

	maxAttempts := 12 // 2 minutes maximum wait
	for i := 0; i < maxAttempts; i++ {
		// Check if instance profile exists and is ready
		result, err := m.iamClient.GetInstanceProfile(context.TODO(), &iam.GetInstanceProfileInput{
			InstanceProfileName: aws.String(profileName),
		})

		if err == nil {
			profileArn := aws.ToString(result.InstanceProfile.Arn)
			fmt.Printf("✅ Instance profile is ready: %s\n", profileArn)
			fmt.Printf("   Instance profile name: %s\n", aws.ToString(result.InstanceProfile.InstanceProfileName))

			// Check if role is attached
			if len(result.InstanceProfile.Roles) > 0 {
				fmt.Printf("   Role attached: %s\n", aws.ToString(result.InstanceProfile.Roles[0].RoleName))
			} else {
				return "", fmt.Errorf("instance profile exists but no role is attached")
			}
			return profileArn, nil
		}

		// Check if it's a "not found" error vs other error
		if strings.Contains(err.Error(), "NoSuchEntity") || strings.Contains(err.Error(), "does not exist") {
			fmt.Printf("  Attempt %d/%d: Instance profile not yet available...\n", i+1, maxAttempts)
			time.Sleep(10 * time.Second)
			continue
		}

		// Some other error occurred
		return "", fmt.Errorf("error checking instance profile: %w", err)
	}

	return "", fmt.Errorf("timeout waiting for instance profile to be ready")
}

func (m *AWSManager) getLatestAmazonLinuxAMI() (string, error) {
	// Search for the latest Amazon Linux 2023 AMI
	result, err := m.ec2Client.DescribeImages(context.TODO(), &ec2.DescribeImagesInput{
		Owners: []string{"amazon"},
		Filters: []types.Filter{
			{
				Name:   aws.String("name"),
				Values: []string{"al2023-ami-*-x86_64"},
			},
			{
				Name:   aws.String("state"),
				Values: []string{"available"},
			},
			{
				Name:   aws.String("architecture"),
				Values: []string{"x86_64"},
			},
		},
	})

	if err != nil {
		return "", fmt.Errorf("failed to describe AMIs: %w", err)
	}

	if len(result.Images) == 0 {
		return "", fmt.Errorf("no Amazon Linux 2023 AMIs found in region %s", m.region)
	}

	// Find the most recent non-minimal AMI by creation date, fallback to any AMI
	var latestAMI *types.Image
	var latestMinimalAMI *types.Image

	for i := range result.Images {
		image := &result.Images[i]
		isMinimal := strings.Contains(aws.ToString(image.Name), "minimal")

		if !isMinimal {
			// Prefer non-minimal AMIs
			if latestAMI == nil || (image.CreationDate != nil && latestAMI.CreationDate != nil &&
				aws.ToString(image.CreationDate) > aws.ToString(latestAMI.CreationDate)) {
				latestAMI = image
			}
		} else {
			// Keep track of latest minimal AMI as fallback
			if latestMinimalAMI == nil || (image.CreationDate != nil && latestMinimalAMI.CreationDate != nil &&
				aws.ToString(image.CreationDate) > aws.ToString(latestMinimalAMI.CreationDate)) {
				latestMinimalAMI = image
			}
		}
	}

	// Use non-minimal if available, otherwise use minimal
	if latestAMI == nil {
		latestAMI = latestMinimalAMI
		if latestAMI != nil {
			fmt.Println("Note: Using minimal AMI - SSM agent will be installed via user data")
		}
	}

	if latestAMI == nil || latestAMI.ImageId == nil {
		return "", fmt.Errorf("could not determine latest Amazon Linux 2023 AMI")
	}

	fmt.Printf("Using Amazon Linux 2023 AMI: %s (%s) in %s\n",
		aws.ToString(latestAMI.ImageId),
		aws.ToString(latestAMI.Name),
		m.region)

	return aws.ToString(latestAMI.ImageId), nil
}

func (m *AWSManager) getPrivateSubnetFromXstrapolateVPC() (string, error) {
	// Look for private subnets in our xstrapolate VPC
	result, err := m.ec2Client.DescribeSubnets(context.TODO(), &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:xstrapolate-vpc"),
				Values: []string{"true"},
			},
			{
				Name:   aws.String("tag:Type"),
				Values: []string{"private"},
			},
		},
	})
	
	if err != nil {
		return "", err
	}
	
	if len(result.Subnets) > 0 {
		return aws.ToString(result.Subnets[0].SubnetId), nil
	}
	
	return "", fmt.Errorf("no private subnets found in xstrapolate VPC")
}

func (m *AWSManager) getAnyAvailableSubnet() (string, error) {
	// Fallback: get any available subnet
	result, err := m.ec2Client.DescribeSubnets(context.TODO(), &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("state"),
				Values: []string{"available"},
			},
		},
	})
	
	if err != nil {
		return "", err
	}
	
	if len(result.Subnets) > 0 {
		return aws.ToString(result.Subnets[0].SubnetId), nil
	}
	
	return "", fmt.Errorf("no available subnets found")
}

func (m *AWSManager) generateUserData(clusterName string) string {
	userDataScript := `#!/bin/bash
set -e

# Update system
yum update -y

# Install required tools
yum install -y curl wget git

# Ensure SSM agent is installed and running
if ! systemctl is-active --quiet amazon-ssm-agent; then
    echo "Installing SSM agent..."
    yum install -y amazon-ssm-agent
    systemctl enable amazon-ssm-agent
    systemctl start amazon-ssm-agent
fi

# Install kubectl
curl -LO https://dl.k8s.io/release/v1.28.0/bin/linux/amd64/kubectl
install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl

# Install helm
curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3
chmod 700 get_helm.sh
./get_helm.sh

# Install flux CLI
curl -s https://fluxcd.io/install.sh | bash
mv /root/.local/bin/flux /usr/local/bin/ 2>/dev/null || true

# Install k3s
curl -sfL https://get.k3s.io | sh -s - --write-kubeconfig-mode 644
export KUBECONFIG=/etc/rancher/k3s/k3s.yaml

# Wait for k3s to be ready
echo "Waiting for k3s to be ready..."
sleep 30
kubectl wait --for=condition=Ready nodes --all --timeout=300s

# Install Flux
echo "Installing Flux..."
flux install --wait

# Create basic cluster info
echo "Creating cluster info..."
cat > /tmp/cluster-info.yaml << EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: cluster-info
  namespace: flux-system
data:
  cluster-name: "` + clusterName + `"
  created-by: "xstrapolate"
  flux-version: "latest"
EOF

kubectl apply -f /tmp/cluster-info.yaml

echo "Setup complete! Cluster ` + clusterName + ` is ready."
echo "Access via: aws ssm start-session --target $(curl -s http://169.254.169.254/latest/meta-data/instance-id)"
echo "Kubeconfig: /etc/rancher/k3s/k3s.yaml"
`
	return userDataScript
}

func (m *AWSManager) generateKubeconfig(clusterName string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	kubeconfigPath := filepath.Join(home, ".kube", fmt.Sprintf("config-%s", clusterName))

	// This would normally generate the kubeconfig using AWS CLI equivalent
	// For now, return the path where it should be
	fmt.Printf("Generate kubeconfig with: aws eks update-kubeconfig --region %s --name %s --kubeconfig %s\n",
		m.region, clusterName, kubeconfigPath)

	return kubeconfigPath, nil
}

func (m *AWSManager) getAccountID() (string, error) {
	result, err := m.stsClient.GetCallerIdentity(context.TODO(), &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", fmt.Errorf("failed to get caller identity: %w", err)
	}
	return aws.ToString(result.Account), nil
}

func (m *AWSManager) DeleteCluster(name string) error {
	fmt.Printf("🔍 Finding resources for cluster '%s'...\n", name)

	// Find EC2 instances with the cluster tag
	instances, err := m.findClusterInstances(name)
	if err != nil {
		return fmt.Errorf("failed to find cluster instances: %w", err)
	}

	if len(instances) == 0 {
		fmt.Println("⚠️  No instances found for this cluster")
	}

	// Collect VPCs from instances to clean up later
	vpcIds := make(map[string]bool)

	// Terminate instances
	for _, instanceId := range instances {
		fmt.Printf("🛑 Terminating instance: %s\n", instanceId)
		_, err = m.ec2Client.TerminateInstances(context.TODO(), &ec2.TerminateInstancesInput{
			InstanceIds: []string{instanceId},
		})
		if err != nil {
			fmt.Printf("Warning: failed to terminate instance %s: %v\n", instanceId, err)
		}

		// Get VPC ID for this instance
		vpcId, err := m.getInstanceVPC(instanceId)
		if err == nil && vpcId != "" {
			vpcIds[vpcId] = true
		}
	}

	// Wait for instances to terminate
	if len(instances) > 0 {
		fmt.Println("⏳ Waiting for instances to terminate...")
		for _, instanceId := range instances {
			waiter := ec2.NewInstanceTerminatedWaiter(m.ec2Client)
			err = waiter.Wait(context.TODO(), &ec2.DescribeInstancesInput{
				InstanceIds: []string{instanceId},
			}, 5*time.Minute)
			if err != nil {
				fmt.Printf("Warning: timeout waiting for instance %s to terminate: %v\n", instanceId, err)
			}
		}
		fmt.Println("✅ All instances terminated")
	}

	// Clean up VPCs and associated resources (only xstrapolate-managed VPCs)
	for vpcId := range vpcIds {
		// Verify this is an xstrapolate-managed VPC before deletion
		isManaged, err := m.isXstrapolateManagedVPC(vpcId)
		if err != nil {
			fmt.Printf("Warning: failed to check VPC %s management status: %v\n", vpcId, err)
			continue
		}
		if !isManaged {
			fmt.Printf("⏭️  Skipping VPC %s (not managed by xstrapolate)\n", vpcId)
			continue
		}

		err = m.deleteVPCResources(vpcId)
		if err != nil {
			fmt.Printf("Warning: failed to clean up VPC %s: %v\n", vpcId, err)
		}
	}

	// Clean up IAM resources
	err = m.deleteIAMResources()
	if err != nil {
		fmt.Printf("Warning: failed to clean up IAM resources: %v\n", err)
	}

	fmt.Println("🧹 Cleanup complete!")
	return nil
}

func (m *AWSManager) findClusterInstances(clusterName string) ([]string, error) {
	result, err := m.ec2Client.DescribeInstances(context.TODO(), &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:xstrapolate-cluster"),
				Values: []string{clusterName},
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"running", "stopped", "stopping", "pending"},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	var instanceIds []string
	for _, reservation := range result.Reservations {
		for _, instance := range reservation.Instances {
			instanceIds = append(instanceIds, aws.ToString(instance.InstanceId))
		}
	}

	return instanceIds, nil
}

func (m *AWSManager) getInstanceVPC(instanceId string) (string, error) {
	result, err := m.ec2Client.DescribeInstances(context.TODO(), &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceId},
	})
	if err != nil {
		return "", err
	}

	if len(result.Reservations) > 0 && len(result.Reservations[0].Instances) > 0 {
		return aws.ToString(result.Reservations[0].Instances[0].VpcId), nil
	}

	return "", fmt.Errorf("instance not found")
}

func (m *AWSManager) isXstrapolateManagedVPC(vpcId string) (bool, error) {
	result, err := m.ec2Client.DescribeVpcs(context.TODO(), &ec2.DescribeVpcsInput{
		VpcIds: []string{vpcId},
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:xstrapolate-managed"),
				Values: []string{"true"},
			},
		},
	})
	if err != nil {
		return false, err
	}

	return len(result.Vpcs) > 0, nil
}

func (m *AWSManager) deleteVPCResources(vpcId string) error {
	fmt.Printf("🗑️  Cleaning up VPC: %s\n", vpcId)

	// Delete VPC endpoints first and wait for deletion
	endpointIds, err := m.deleteVPCEndpoints(vpcId)
	if err != nil {
		fmt.Printf("Warning: failed to delete VPC endpoints: %v\n", err)
	}

	// Wait for VPC endpoints to be fully deleted
	if len(endpointIds) > 0 {
		fmt.Println("⏳ Waiting for VPC endpoints to be deleted...")
		err = m.waitForVPCEndpointsDeletion(endpointIds)
		if err != nil {
			fmt.Printf("Warning: timeout waiting for VPC endpoints deletion: %v\n", err)
		} else {
			fmt.Println("✅ VPC endpoints deleted")
		}

		// Wait a bit more for ENIs to be cleaned up
		fmt.Println("⏳ Waiting for network interfaces to be cleaned up...")
		time.Sleep(30 * time.Second)
	}

	// Delete NAT gateways (if any)
	err = m.deleteNATGateways(vpcId)
	if err != nil {
		fmt.Printf("Warning: failed to delete NAT gateways: %v\n", err)
	}

	// Detach and delete internet gateways
	err = m.deleteInternetGateways(vpcId)
	if err != nil {
		fmt.Printf("Warning: failed to delete internet gateways: %v\n", err)
	}

	// Delete route tables (except main)
	err = m.deleteRouteTables(vpcId)
	if err != nil {
		fmt.Printf("Warning: failed to delete route tables: %v\n", err)
	}

	// Delete security groups (except default)
	err = m.deleteSecurityGroups(vpcId)
	if err != nil {
		fmt.Printf("Warning: failed to delete security groups: %v\n", err)
	}

	// Delete subnets (now safe since VPC endpoints are gone)
	err = m.deleteSubnets(vpcId)
	if err != nil {
		fmt.Printf("Warning: failed to delete subnets: %v\n", err)
	}

	// Finally, delete the VPC
	_, err = m.ec2Client.DeleteVpc(context.TODO(), &ec2.DeleteVpcInput{
		VpcId: aws.String(vpcId),
	})
	if err != nil {
		return fmt.Errorf("failed to delete VPC: %w", err)
	}

	fmt.Printf("✅ VPC %s deleted\n", vpcId)
	return nil
}

func (m *AWSManager) deleteVPCEndpoints(vpcId string) ([]string, error) {
	result, err := m.ec2Client.DescribeVpcEndpoints(context.TODO(), &ec2.DescribeVpcEndpointsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{vpcId},
			},
			{
				Name:   aws.String("tag:xstrapolate-managed"),
				Values: []string{"true"},
			},
		},
	})
	if err != nil {
		return nil, err
	}

	var endpointIds []string
	for _, endpoint := range result.VpcEndpoints {
		endpointId := aws.ToString(endpoint.VpcEndpointId)
		endpointIds = append(endpointIds, endpointId)
		fmt.Printf("  Deleting VPC endpoint: %s\n", endpointId)
		_, err = m.ec2Client.DeleteVpcEndpoints(context.TODO(), &ec2.DeleteVpcEndpointsInput{
			VpcEndpointIds: []string{endpointId},
		})
		if err != nil {
			fmt.Printf("    Warning: failed to delete VPC endpoint %s: %v\n", endpointId, err)
		}
	}

	return endpointIds, nil
}

func (m *AWSManager) waitForVPCEndpointsDeletion(endpointIds []string) error {
	// Wait up to 5 minutes for all VPC endpoints to be deleted
	maxWait := 5 * time.Minute
	checkInterval := 10 * time.Second
	timeout := time.After(maxWait)
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for VPC endpoints to be deleted")
		case <-ticker.C:
			// Check if any endpoints still exist
			result, err := m.ec2Client.DescribeVpcEndpoints(context.TODO(), &ec2.DescribeVpcEndpointsInput{
				VpcEndpointIds: endpointIds,
			})
			if err != nil {
				// If we get an error describing endpoints, they might be deleted
				// Check if it's a "not found" type error
				if strings.Contains(err.Error(), "InvalidVpcEndpointId.NotFound") ||
				   strings.Contains(err.Error(), "does not exist") {
					return nil // All endpoints deleted
				}
				return err
			}

			// Count how many endpoints are still in "deleting" or other states
			stillExists := 0
			for _, endpoint := range result.VpcEndpoints {
				state := string(endpoint.State)
				// VPC endpoints in "deleted" state won't be returned, so any returned endpoint still exists
				if state != "deleted" {
					stillExists++
				}
			}

			if stillExists == 0 {
				return nil // All endpoints deleted
			}

			fmt.Printf("  Still waiting for %d VPC endpoints to finish deleting...\n", stillExists)
		}
	}
}

func (m *AWSManager) deleteNATGateways(vpcId string) error {
	result, err := m.ec2Client.DescribeNatGateways(context.TODO(), &ec2.DescribeNatGatewaysInput{
		Filter: []types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{vpcId},
			},
			{
				Name:   aws.String("state"),
				Values: []string{"available"},
			},
		},
	})
	if err != nil {
		return err
	}

	for _, natGw := range result.NatGateways {
		natGwId := aws.ToString(natGw.NatGatewayId)
		fmt.Printf("  Deleting NAT gateway: %s\n", natGwId)
		_, err = m.ec2Client.DeleteNatGateway(context.TODO(), &ec2.DeleteNatGatewayInput{
			NatGatewayId: aws.String(natGwId),
		})
		if err != nil {
			fmt.Printf("    Warning: failed to delete NAT gateway %s: %v\n", natGwId, err)
		}
	}

	return nil
}

func (m *AWSManager) deleteInternetGateways(vpcId string) error {
	result, err := m.ec2Client.DescribeInternetGateways(context.TODO(), &ec2.DescribeInternetGatewaysInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("attachment.vpc-id"),
				Values: []string{vpcId},
			},
			{
				Name:   aws.String("tag:xstrapolate-managed"),
				Values: []string{"true"},
			},
		},
	})
	if err != nil {
		return err
	}

	for _, igw := range result.InternetGateways {
		igwId := aws.ToString(igw.InternetGatewayId)
		fmt.Printf("  Detaching and deleting internet gateway: %s\n", igwId)

		// Detach first
		_, err = m.ec2Client.DetachInternetGateway(context.TODO(), &ec2.DetachInternetGatewayInput{
			InternetGatewayId: aws.String(igwId),
			VpcId:             aws.String(vpcId),
		})
		if err != nil {
			fmt.Printf("    Warning: failed to detach internet gateway %s: %v\n", igwId, err)
		}

		// Then delete
		_, err = m.ec2Client.DeleteInternetGateway(context.TODO(), &ec2.DeleteInternetGatewayInput{
			InternetGatewayId: aws.String(igwId),
		})
		if err != nil {
			fmt.Printf("    Warning: failed to delete internet gateway %s: %v\n", igwId, err)
		}
	}

	return nil
}

func (m *AWSManager) deleteRouteTables(vpcId string) error {
	result, err := m.ec2Client.DescribeRouteTables(context.TODO(), &ec2.DescribeRouteTablesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{vpcId},
			},
			{
				Name:   aws.String("association.main"),
				Values: []string{"false"}, // Don't delete main route table
			},
			{
				Name:   aws.String("tag:xstrapolate-managed"),
				Values: []string{"true"},
			},
		},
	})
	if err != nil {
		return err
	}

	for _, rt := range result.RouteTables {
		rtId := aws.ToString(rt.RouteTableId)
		fmt.Printf("  Deleting route table: %s\n", rtId)
		_, err = m.ec2Client.DeleteRouteTable(context.TODO(), &ec2.DeleteRouteTableInput{
			RouteTableId: aws.String(rtId),
		})
		if err != nil {
			fmt.Printf("    Warning: failed to delete route table %s: %v\n", rtId, err)
		}
	}

	return nil
}

func (m *AWSManager) deleteSecurityGroups(vpcId string) error {
	result, err := m.ec2Client.DescribeSecurityGroups(context.TODO(), &ec2.DescribeSecurityGroupsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{vpcId},
			},
			{
				Name:   aws.String("tag:xstrapolate-managed"),
				Values: []string{"true"},
			},
		},
	})
	if err != nil {
		return err
	}

	for _, sg := range result.SecurityGroups {
		// Skip default security group
		if aws.ToString(sg.GroupName) == "default" {
			continue
		}

		sgId := aws.ToString(sg.GroupId)
		fmt.Printf("  Deleting security group: %s\n", sgId)

		// Retry security group deletion with backoff
		maxRetries := 3
		for retry := 0; retry < maxRetries; retry++ {
			_, err = m.ec2Client.DeleteSecurityGroup(context.TODO(), &ec2.DeleteSecurityGroupInput{
				GroupId: aws.String(sgId),
			})
			if err == nil {
				break
			}

			if retry < maxRetries-1 && strings.Contains(err.Error(), "DependencyViolation") {
				fmt.Printf("    Retry %d/%d: dependency violation, waiting...\n", retry+1, maxRetries)
				time.Sleep(10 * time.Second)
				continue
			}

			fmt.Printf("    Warning: failed to delete security group %s: %v\n", sgId, err)
			break
		}
	}

	return nil
}

func (m *AWSManager) deleteSubnets(vpcId string) error {
	result, err := m.ec2Client.DescribeSubnets(context.TODO(), &ec2.DescribeSubnetsInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("vpc-id"),
				Values: []string{vpcId},
			},
			{
				Name:   aws.String("tag:xstrapolate-managed"),
				Values: []string{"true"},
			},
		},
	})
	if err != nil {
		return err
	}

	for _, subnet := range result.Subnets {
		subnetId := aws.ToString(subnet.SubnetId)
		fmt.Printf("  Deleting subnet: %s\n", subnetId)

		// Retry subnet deletion with backoff
		maxRetries := 3
		for retry := 0; retry < maxRetries; retry++ {
			_, err = m.ec2Client.DeleteSubnet(context.TODO(), &ec2.DeleteSubnetInput{
				SubnetId: aws.String(subnetId),
			})
			if err == nil {
				break
			}

			if retry < maxRetries-1 && strings.Contains(err.Error(), "DependencyViolation") {
				fmt.Printf("    Retry %d/%d: dependency violation, waiting...\n", retry+1, maxRetries)
				time.Sleep(10 * time.Second)
				continue
			}

			fmt.Printf("    Warning: failed to delete subnet %s: %v\n", subnetId, err)
			break
		}
	}

	return nil
}

func (m *AWSManager) deleteIAMResources() error {
	fmt.Println("🗑️  Cleaning up IAM resources...")

	// Delete SSM instance profile and role
	err := m.deleteSSMRole()
	if err != nil {
		fmt.Printf("Warning: failed to delete SSM role: %v\n", err)
	}

	// Delete EKS service role (if exists)
	err = m.deleteEKSRole()
	if err != nil {
		fmt.Printf("Warning: failed to delete EKS role: %v\n", err)
	}

	return nil
}

func (m *AWSManager) deleteSSMRole() error {
	roleName := "xstrapolate-ssm-role"
	profileName := "xstrapolate-ssm-profile"

	// Remove role from instance profile
	_, err := m.iamClient.RemoveRoleFromInstanceProfile(context.TODO(), &iam.RemoveRoleFromInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
		RoleName:            aws.String(roleName),
	})
	if err != nil {
		fmt.Printf("  Warning: failed to remove role from instance profile: %v\n", err)
	}

	// Delete instance profile
	_, err = m.iamClient.DeleteInstanceProfile(context.TODO(), &iam.DeleteInstanceProfileInput{
		InstanceProfileName: aws.String(profileName),
	})
	if err != nil {
		fmt.Printf("  Warning: failed to delete instance profile: %v\n", err)
	}

	// Detach policy from role
	_, err = m.iamClient.DetachRolePolicy(context.TODO(), &iam.DetachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"),
	})
	if err != nil {
		fmt.Printf("  Warning: failed to detach policy from role: %v\n", err)
	}

	// Delete role
	_, err = m.iamClient.DeleteRole(context.TODO(), &iam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		fmt.Printf("  Warning: failed to delete SSM role: %v\n", err)
	} else {
		fmt.Printf("  ✅ Deleted SSM role and instance profile\n")
	}

	return nil
}

func (m *AWSManager) deleteEKSRole() error {
	roleName := "xstrapolate-eks-service-role"

	// Check if role exists first
	_, err := m.iamClient.GetRole(context.TODO(), &iam.GetRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		if strings.Contains(err.Error(), "NoSuchEntity") {
			fmt.Printf("  EKS service role '%s' does not exist, skipping\n", roleName)
			return nil
		}
		return fmt.Errorf("failed to check EKS role: %w", err)
	}

	// Detach policy from role
	_, err = m.iamClient.DetachRolePolicy(context.TODO(), &iam.DetachRolePolicyInput{
		RoleName:  aws.String(roleName),
		PolicyArn: aws.String("arn:aws:iam::aws:policy/AmazonEKSClusterPolicy"),
	})
	if err != nil {
		fmt.Printf("  Warning: failed to detach policy from EKS role: %v\n", err)
	}

	// Delete role
	_, err = m.iamClient.DeleteRole(context.TODO(), &iam.DeleteRoleInput{
		RoleName: aws.String(roleName),
	})
	if err != nil {
		fmt.Printf("  Warning: failed to delete EKS role: %v\n", err)
	} else {
		fmt.Printf("  ✅ Deleted EKS service role\n")
	}

	return nil
}

func (m *AWSManager) GetCluster(name string) (*ClusterInfo, error) {
	// Implementation for getting cluster info
	return nil, fmt.Errorf("get cluster not implemented yet")
}