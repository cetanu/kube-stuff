package main

import (
	"encoding/base64"
	"encoding/json"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/autoscaling"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// --- CONFIGURATION / PARAMETERS ---
		cfg := config.New(ctx, "")

		// KeyPairName is required (no default in original CFN template)
		keyPairName := cfg.Require("keyPairName")

		// Optional parameters with defaults matching cfn.yml
		yourIP := cfg.Get("yourIP")
		if yourIP == "" {
			yourIP = "0.0.0.0/0"
		}

		gitHubOrg := cfg.Get("gitHubOrg")
		if gitHubOrg == "" {
			gitHubOrg = "my-org"
		}

		gitHubRepo := cfg.Get("gitHubRepo")
		if gitHubRepo == "" {
			gitHubRepo = "my-repo"
		}

		podCidr := cfg.Get("podCidr")
		if podCidr == "" {
			podCidr = "10.244.0.0/16"
		}

		workerVolumeSize := cfg.GetInt("workerVolumeSize")
		if workerVolumeSize == 0 {
			workerVolumeSize = 16
		}

		// --- DYNAMIC AWS LOOKUPS ---
		// Fetch caller identity (Account ID)
		callerIdentity, err := aws.GetCallerIdentity(ctx, nil)
		if err != nil {
			return err
		}

		// Fetch current region
		currentRegion, err := aws.GetRegion(ctx, nil)
		if err != nil {
			return err
		}

		// Fetch availability zones in the current region
		zones, err := aws.GetAvailabilityZones(ctx, &aws.GetAvailabilityZonesArgs{
			State: pulumi.StringRef("available"),
		})
		if err != nil {
			return err
		}

		// Fetch the latest Ubuntu 22.04 LTS AMI dynamically for the active region
		ami, err := ec2.LookupAmi(ctx, &ec2.LookupAmiArgs{
			MostRecent: pulumi.BoolRef(true),
			Filters: []ec2.GetAmiFilter{
				{
					Name:   "name",
					Values: []string{"ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"},
				},
				{
					Name:   "virtualization-type",
					Values: []string{"hvm"},
				},
			},
			Owners: []string{"099720109477"}, // Canonical owner ID
		}, nil)
		if err != nil {
			return err
		}

		// --- IAM ROLE CONFIGURATION ---
		assumeRolePolicy, err := json.Marshal(map[string]interface{}{
			"Version": "2012-10-17",
			"Statement": []map[string]interface{}{
				{
					"Effect": "Allow",
					"Principal": map[string]interface{}{
						"Service": "ec2.amazonaws.com",
					},
					"Action": "sts:AssumeRole",
				},
			},
		})
		if err != nil {
			return err
		}

		instanceRole, err := iam.NewRole(ctx, "instance-role", &iam.RoleArgs{
			AssumeRolePolicy: pulumi.String(string(assumeRolePolicy)),
			ManagedPolicyArns: pulumi.StringArray{
				pulumi.String("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"),
			},
		})
		if err != nil {
			return err
		}

		// Inline Policy: KubeadmJoinTokenExchange
		kubeadmJoinPolicyDoc := pulumi.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Action": [
						"ssm:PutParameter",
						"ssm:GetParameter"
					],
					"Resource": "arn:aws:ssm:%s:%s:parameter/kubeadm/*"
				},
				{
					"Effect": "Allow",
					"Action": [
						"ec2:DescribeTags"
					],
					"Resource": "*"
				},
				{
					"Effect": "Allow",
					"Action": [
						"cloudformation:SignalResource"
					],
					"Resource": "arn:aws:cloudformation:%s:%s:stack/%s/*"
				}
			]
		}`, currentRegion.Name, callerIdentity.AccountId, currentRegion.Name, callerIdentity.AccountId, ctx.Stack())

		_, err = iam.NewRolePolicy(ctx, "kubeadm-join-token-exchange", &iam.RolePolicyArgs{
			Role:   instanceRole.Name,
			Policy: kubeadmJoinPolicyDoc,
		})
		if err != nil {
			return err
		}

		// Inline Policy: AWSCloudProvider
		cloudProviderPolicyDoc := `{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Action": [
						"autoscaling:DescribeAutoScalingGroups",
						"autoscaling:DescribeLaunchConfigurations",
						"autoscaling:DescribeTags",
						"ec2:DescribeInstances",
						"ec2:DescribeRegions",
						"ec2:DescribeRouteTables",
						"ec2:DescribeSecurityGroups",
						"ec2:DescribeSubnets",
						"ec2:DescribeVolumes",
						"ec2:DescribeAvailabilityZones",
						"ec2:DescribeVpcs",
						"ec2:CreateSecurityGroup",
						"ec2:CreateTags",
						"ec2:CreateVolume",
						"ec2:ModifyInstanceAttribute",
						"ec2:ModifyVolume",
						"ec2:AttachVolume",
						"ec2:AuthorizeSecurityGroupIngress",
						"ec2:CreateRoute",
						"ec2:DeleteRoute",
						"ec2:DeleteSecurityGroup",
						"ec2:DeleteVolume",
						"ec2:DetachVolume",
						"ec2:RevokeSecurityGroupIngress",
						"elasticloadbalancing:*"
					],
					"Resource": "*"
				}
			]
		}`

		_, err = iam.NewRolePolicy(ctx, "aws-cloud-provider", &iam.RolePolicyArgs{
			Role:   instanceRole.Name,
			Policy: pulumi.String(cloudProviderPolicyDoc),
		})
		if err != nil {
			return err
		}

		// Inline Policy: BootstrapLogsUpload
		bootstrapLogsPolicyDoc := pulumi.Sprintf(`{
			"Version": "2012-10-17",
			"Statement": [
				{
					"Effect": "Allow",
					"Action": [
						"s3:PutObject"
					],
					"Resource": "arn:aws:s3:::k8s-bootstrap-logs-%s-%s/*"
				}
			]
		}`, callerIdentity.AccountId, currentRegion.Name)

		_, err = iam.NewRolePolicy(ctx, "bootstrap-logs-upload", &iam.RolePolicyArgs{
			Role:   instanceRole.Name,
			Policy: bootstrapLogsPolicyDoc,
		})
		if err != nil {
			return err
		}

		instanceProfile, err := iam.NewInstanceProfile(ctx, "instance-profile", &iam.InstanceProfileArgs{
			Role: instanceRole.Name,
		})
		if err != nil {
			return err
		}

		// --- NETWORKING ---
		vpc, err := ec2.NewVpc(ctx, "vpc", &ec2.VpcArgs{
			CidrBlock:          pulumi.String("10.240.0.0/16"),
			EnableDnsSupport:   pulumi.Bool(true),
			EnableDnsHostnames: pulumi.Bool(true),
			Tags: pulumi.StringMap{
				"Name":                             pulumi.String("k8s-modern-vpc"),
				"kubernetes.io/cluster/kubernetes": pulumi.String("owned"),
			},
		})
		if err != nil {
			return err
		}

		internetGateway, err := ec2.NewInternetGateway(ctx, "internet-gateway", &ec2.InternetGatewayArgs{
			VpcId: vpc.ID(),
		})
		if err != nil {
			return err
		}

		subnet, err := ec2.NewSubnet(ctx, "subnet", &ec2.SubnetArgs{
			VpcId:               vpc.ID(),
			CidrBlock:           pulumi.String("10.240.0.0/24"),
			MapPublicIpOnLaunch: pulumi.Bool(true),
			AvailabilityZone:    pulumi.String(zones.Names[0]),
			Tags: pulumi.StringMap{
				"Name":                             pulumi.String("k8s-modern-subnet"),
				"kubernetes.io/cluster/kubernetes": pulumi.String("owned"),
				"kubernetes.io/role/elb":           pulumi.String("1"),
			},
		})
		if err != nil {
			return err
		}

		routeTable, err := ec2.NewRouteTable(ctx, "route-table", &ec2.RouteTableArgs{
			VpcId: vpc.ID(),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewRoute(ctx, "default-route", &ec2.RouteArgs{
			RouteTableId:         routeTable.ID(),
			DestinationCidrBlock: pulumi.String("0.0.0.0/0"),
			GatewayId:            internetGateway.ID(),
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewRouteTableAssociation(ctx, "subnet-route-table-association", &ec2.RouteTableAssociationArgs{
			SubnetId:     subnet.ID(),
			RouteTableId: routeTable.ID(),
		})
		if err != nil {
			return err
		}

		clusterSG, err := ec2.NewSecurityGroup(ctx, "cluster-sg", &ec2.SecurityGroupArgs{
			VpcId:       vpc.ID(),
			Description: pulumi.String("Allow all internal traffic, plus external SSH and Kubernetes API"),
			Ingress: ec2.SecurityGroupIngressArray{
				// Full internal cluster communication (Nodes talking to each other)
				&ec2.SecurityGroupIngressArgs{
					Protocol: pulumi.String("-1"),
					FromPort: pulumi.Int(0),
					ToPort:   pulumi.Int(0),
					CidrBlocks: pulumi.StringArray{
						pulumi.String("10.240.0.0/24"),
					},
				},
				// Flannel overlay pod network communication space
				&ec2.SecurityGroupIngressArgs{
					Protocol: pulumi.String("-1"),
					FromPort: pulumi.Int(0),
					ToPort:   pulumi.Int(0),
					CidrBlocks: pulumi.StringArray{
						pulumi.String(podCidr),
					},
				},
				// External management access
				&ec2.SecurityGroupIngressArgs{
					Protocol: pulumi.String("tcp"),
					FromPort: pulumi.Int(22),
					ToPort:   pulumi.Int(22),
					CidrBlocks: pulumi.StringArray{
						pulumi.String(yourIP),
					},
				},
				// Valheim standard UDP ports
				&ec2.SecurityGroupIngressArgs{
					Protocol: pulumi.String("udp"),
					FromPort: pulumi.Int(2456),
					ToPort:   pulumi.Int(2457),
					CidrBlocks: pulumi.StringArray{
						pulumi.String("0.0.0.0/0"),
					},
				},
				// Kubernetes NodePorts (UDP) - required if Envoy provisions a NodePort Service
				&ec2.SecurityGroupIngressArgs{
					Protocol: pulumi.String("udp"),
					FromPort: pulumi.Int(30000),
					ToPort:   pulumi.Int(32767),
					CidrBlocks: pulumi.StringArray{
						pulumi.String("0.0.0.0/0"),
					},
				},
				// Kubernetes NodePorts (TCP)
				&ec2.SecurityGroupIngressArgs{
					Protocol: pulumi.String("tcp"),
					FromPort: pulumi.Int(30000),
					ToPort:   pulumi.Int(32767),
					CidrBlocks: pulumi.StringArray{
						pulumi.String("0.0.0.0/0"),
					},
				},
			},
			Egress: ec2.SecurityGroupEgressArray{
				&ec2.SecurityGroupEgressArgs{
					Protocol:   pulumi.String("-1"),
					FromPort:   pulumi.Int(0),
					ToPort:     pulumi.Int(0),
					CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				},
			},
		})
		if err != nil {
			return err
		}

		_, err = ssm.NewParameter(ctx, "cluster-sg-parameter", &ssm.ParameterArgs{
			Name:        pulumi.String("/kubeadm/security-group-id"),
			Type:        pulumi.String("String"),
			Value:       clusterSG.ID(),
			Description: pulumi.String("Security Group ID of the Kubernetes Cluster"),
		})
		if err != nil {
			return err
		}

		controlPlaneEip, err := ec2.NewEip(ctx, "control-plane-eip", &ec2.EipArgs{
			Domain: pulumi.String("vpc"),
			Tags: pulumi.StringMap{
				"Name": pulumi.String("control-plane-eip"),
			},
		})
		if err != nil {
			return err
		}

		// --- USERDATA FOR CONTROL PLANE ---
		controlPlaneUserDataTemplate := `#!/bin/bash
set -eux
# ------------- Setup Signaling --------------
TOKEN=$(curl -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 21600" -s)
INSTANCE_ID=$(curl -H "X-aws-ec2-metadata-token: $TOKEN" -s http://169.254.169.254/latest/meta-data/instance-id)
# Note: CloudFormation signal-resource commands removed due to migration to Pulumi.
trap 'if command -v aws >/dev/null 2>&1; then aws s3 cp /var/log/cloud-init-output.log s3://k8s-bootstrap-logs-%s-%s/logs/$INSTANCE_ID-controlplane.log --region %s || true; fi' ERR

# ------------- Network check --------------
until curl -s --connect-timeout 2 aws.amazon.com > /dev/null; do
  sleep 2
done

# ------------- Bootstrap SaltStack --------------
apt-get update && apt-get install -y curl git awscli jq
mkdir -m 755 -p /etc/apt/keyrings
curl -fsSL https://packages.broadcom.com/artifactory/api/security/keypair/SaltProjectKey/public | gpg --dearmor | sudo tee /etc/apt/keyrings/salt-archive-keyring.pgp > /dev/null
curl -fsSL https://github.com/saltstack/salt-install-guide/releases/latest/download/salt.sources | sudo tee /etc/apt/sources.list.d/salt.sources
apt-get update
curl -o bootstrap-salt.sh -fsSL https://github.com/saltstack/salt-bootstrap/releases/latest/download/bootstrap-salt.sh
sh bootstrap-salt.sh -U -P stable 3006.1
rm -rf /srv/salt-repo
git clone https://github.com/%s/%s.git /srv/salt-repo

# -------------- Set up Salt grains --------------
AZ=$(curl -H "X-aws-ec2-metadata-token: $TOKEN" -s http://169.254.169.254/latest/meta-data/placement/availability-zone)
ROLE=$(aws ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Role" --region %s --query "Tags[0].Value" --output text)
echo "role: $ROLE" > /etc/salt/grains
echo "provider_id: aws:///$AZ/$INSTANCE_ID" >> /etc/salt/grains

# -------------- Initial Bootstrap Run --------------
salt-call --local --file-root=/srv/salt-repo/salt state.apply pillar="{aws_region: '%s', eip: '%s', pod_cidr: '%s'}"

if [ "$ROLE" == "controlplane" ]; then
  LOCAL=$(git -C /srv/salt-repo rev-parse HEAD)
  aws ssm put-parameter --name "/kubeadm/salt-status" --value "$LOCAL" --type "String" --overwrite --region %s
fi
`

		controlPlaneUserData := pulumi.Sprintf(
			controlPlaneUserDataTemplate,
			callerIdentity.AccountId,
			currentRegion.Name,
			currentRegion.Name,
			pulumi.String(gitHubOrg),
			pulumi.String(gitHubRepo),
			currentRegion.Name,
			currentRegion.Name,
			controlPlaneEip.PublicIp,
			pulumi.String(podCidr),
			currentRegion.Name,
		)

		controlPlane, err := ec2.NewInstance(ctx, "control-plane-0", &ec2.InstanceArgs{
			Ami:                 pulumi.String(ami.Id),
			InstanceType:        pulumi.String("t3.medium"),
			KeyName:             pulumi.String(keyPairName),
			IamInstanceProfile:  instanceProfile.Name,
			VpcSecurityGroupIds: pulumi.StringArray{clusterSG.ID()},
			SubnetId:            subnet.ID(),
			PrivateIp:           pulumi.String("10.240.0.11"),
			Tags: pulumi.StringMap{
				"Name":                             pulumi.String("control-plane-0"),
				"kubernetes.io/cluster/kubernetes": pulumi.String("owned"),
				"Role":                             pulumi.String("controlplane"),
			},
			UserData: controlPlaneUserData,
		})
		if err != nil {
			return err
		}

		_, err = ec2.NewEipAssociation(ctx, "control-plane-eip-association", &ec2.EipAssociationArgs{
			InstanceId:   controlPlane.ID(),
			AllocationId: controlPlaneEip.AllocationId,
		})
		if err != nil {
			return err
		}

		// --- USERDATA FOR WORKER NODES ---
		workerUserDataTemplate := `#!/bin/bash
set -eux
# ------------- Setup Signaling --------------
TOKEN=$(curl -X PUT "http://169.254.169.254/latest/api/token" -H "X-aws-ec2-metadata-token-ttl-seconds: 21600" -s)
INSTANCE_ID=$(curl -H "X-aws-ec2-metadata-token: $TOKEN" -s http://169.254.169.254/latest/meta-data/instance-id)
# Note: CloudFormation signal-resource commands removed due to migration to Pulumi.
trap 'if command -v aws >/dev/null 2>&1; then aws s3 cp /var/log/cloud-init-output.log s3://k8s-bootstrap-logs-%s-%s/logs/$INSTANCE_ID-worker.log --region %s || true; fi' ERR

# ------------- Network check --------------
until curl -s --connect-timeout 2 aws.amazon.com > /dev/null; do
  sleep 2
done

# ------------- Bootstrap SaltStack --------------
apt-get update && apt-get install -y curl git awscli jq
mkdir -m 755 -p /etc/apt/keyrings
curl -fsSL https://packages.broadcom.com/artifactory/api/security/keypair/SaltProjectKey/public | gpg --dearmor | sudo tee /etc/apt/keyrings/salt-archive-keyring.pgp > /dev/null
curl -fsSL https://github.com/saltstack/salt-install-guide/releases/latest/download/salt.sources | sudo tee /etc/apt/sources.list.d/salt.sources
apt-get update
curl -o bootstrap-salt.sh -fsSL https://github.com/saltstack/salt-bootstrap/releases/latest/download/bootstrap-salt.sh
sh bootstrap-salt.sh -U -P stable 3006.1
rm -rf /srv/salt-repo
git clone https://github.com/%s/%s.git /srv/salt-repo

# -------------- Set up Salt grains --------------
AZ=$(curl -H "X-aws-ec2-metadata-token: $TOKEN" -s http://169.254.169.254/latest/meta-data/placement/availability-zone)
echo "role: worker" > /etc/salt/grains
echo "provider_id: aws:///$AZ/$INSTANCE_ID" >> /etc/salt/grains

# -------------- Initial Bootstrap Run --------------
salt-call --local --file-root=/srv/salt-repo/salt state.apply pillar="{aws_region: '%s', eip: '%s', pod_cidr: '%s'}"
`

		workerUserData := pulumi.Sprintf(
			workerUserDataTemplate,
			callerIdentity.AccountId,
			currentRegion.Name,
			currentRegion.Name,
			pulumi.String(gitHubOrg),
			pulumi.String(gitHubRepo),
			currentRegion.Name,
			controlPlaneEip.PublicIp,
			pulumi.String(podCidr),
		)

		workerUserDataBase64 := workerUserData.ToStringOutput().ApplyT(func(s string) string {
			return base64.StdEncoding.EncodeToString([]byte(s))
		}).(pulumi.StringOutput)

		workerLaunchTemplate, err := ec2.NewLaunchTemplate(ctx, "worker-launch-template", &ec2.LaunchTemplateArgs{
			Name:         pulumi.String(ctx.Stack() + "-worker-launch-template"),
			ImageId:      pulumi.String(ami.Id),
			InstanceType: pulumi.String("t3.medium"),
			KeyName:      pulumi.String(keyPairName),
			IamInstanceProfile: &ec2.LaunchTemplateIamInstanceProfileArgs{
				Arn: instanceProfile.Arn,
			},
			VpcSecurityGroupIds: pulumi.StringArray{clusterSG.ID()},
			BlockDeviceMappings: ec2.LaunchTemplateBlockDeviceMappingArray{
				&ec2.LaunchTemplateBlockDeviceMappingArgs{
					DeviceName: pulumi.String("/dev/sda1"),
					Ebs: &ec2.LaunchTemplateBlockDeviceMappingEbsArgs{
						VolumeSize: pulumi.Int(workerVolumeSize),
						VolumeType: pulumi.String("gp3"),
					},
				},
			},
			TagSpecifications: ec2.LaunchTemplateTagSpecificationArray{
				&ec2.LaunchTemplateTagSpecificationArgs{
					ResourceType: pulumi.String("instance"),
					Tags: pulumi.StringMap{
						"Name":                             pulumi.String("worker-node"),
						"kubernetes.io/cluster/kubernetes": pulumi.String("owned"),
						"Role":                             pulumi.String("worker"),
					},
				},
			},
			UserData: workerUserDataBase64,
		}, pulumi.DependsOn([]pulumi.Resource{controlPlane}))
		if err != nil {
			return err
		}

		_, err = autoscaling.NewGroup(ctx, "worker-asg", &autoscaling.GroupArgs{
			VpcZoneIdentifiers: pulumi.StringArray{
				subnet.ID(),
			},
			LaunchTemplate: &autoscaling.GroupLaunchTemplateArgs{
				Id:      workerLaunchTemplate.ID(),
				Version: pulumi.String("$Latest"),
			},
			MinSize:         pulumi.Int(2),
			MaxSize:         pulumi.Int(4),
			DesiredCapacity: pulumi.Int(2),
			InstanceRefresh: &autoscaling.GroupInstanceRefreshArgs{
				Strategy: pulumi.String("Rolling"),
				Preferences: &autoscaling.GroupInstanceRefreshPreferencesArgs{
					MinHealthyPercentage: pulumi.Int(50),
				},
			},
			Tags: autoscaling.GroupTagArray{
				&autoscaling.GroupTagArgs{
					Key:               pulumi.String("Name"),
					Value:             pulumi.String("worker-node"),
					PropagateAtLaunch: pulumi.Bool(true),
				},
				&autoscaling.GroupTagArgs{
					Key:               pulumi.String("kubernetes.io/cluster/kubernetes"),
					Value:             pulumi.String("owned"),
					PropagateAtLaunch: pulumi.Bool(true),
				},
				&autoscaling.GroupTagArgs{
					Key:               pulumi.String("Role"),
					Value:             pulumi.String("worker"),
					PropagateAtLaunch: pulumi.Bool(true),
				},
			},
		}, pulumi.DependsOn([]pulumi.Resource{controlPlane}))
		if err != nil {
			return err
		}

		// --- EXPORTS ---
		ctx.Export("vpcId", vpc.ID())
		ctx.Export("subnetId", subnet.ID())
		ctx.Export("controlPlanePublicIp", controlPlaneEip.PublicIp)
		ctx.Export("securityGroupId", clusterSG.ID())

		return nil
	})
}
