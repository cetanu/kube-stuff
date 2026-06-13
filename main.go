package main

import (
	"encoding/json"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/autoscaling"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/lb"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// --- CONFIGURATION / PARAMETERS ---
		cfg := config.New(ctx, "")


		podCidr := cfg.Get("podCidr")
		if podCidr == "" {
			podCidr = "10.244.0.0/16"
		}

		workerVolumeSize := cfg.GetInt("workerVolumeSize")
		if workerVolumeSize == 0 {
			workerVolumeSize = 16
		}

		// --- DYNAMIC AWS LOOKUPS ---
		zones, err := aws.GetAvailabilityZones(ctx, &aws.GetAvailabilityZonesArgs{
			State: pulumi.StringRef("available"),
		})
		if err != nil {
			return err
		}

		// Talos Linux AMI
		amiId := "ami-01af9407a2f0b0150"

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

		// Inline Policy: AWSCloudProvider (necessary for Kubernetes-AWS integration)
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

		// Security Group for Kubernetes API Server Load Balancer
		apiServerLBSG, err := ec2.NewSecurityGroup(ctx, "api-server-lb-sg", &ec2.SecurityGroupArgs{
			VpcId:       vpc.ID(),
			Description: pulumi.String("Security Group for Kubernetes API Server Load Balancer"),
			// No ingress rules by default - will be opened ad-hoc by GitHub Action runner
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

				// Inbound Kubernetes API from the Load Balancer Security Group
				&ec2.SecurityGroupIngressArgs{
					Protocol: pulumi.String("tcp"),
					FromPort: pulumi.Int(6443),
					ToPort:   pulumi.Int(6443),
					SecurityGroups: pulumi.StringArray{
						apiServerLBSG.ID(),
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

		controlPlane, err := ec2.NewInstance(ctx, "control-plane-0", &ec2.InstanceArgs{
			Ami:                 pulumi.String(amiId),
			InstanceType:        pulumi.String("t3.medium"),

			IamInstanceProfile:  instanceProfile.Name,
			VpcSecurityGroupIds: pulumi.StringArray{clusterSG.ID()},
			SubnetId:            subnet.ID(),
			PrivateIp:           pulumi.String("10.240.0.11"),
			Tags: pulumi.StringMap{
				"Name":                             pulumi.String("control-plane-0"),
				"kubernetes.io/cluster/kubernetes": pulumi.String("owned"),
				"Role":                             pulumi.String("controlplane"),
			},
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

		// --- KUBERNETES API SERVER LOAD BALANCER ---
		apiServerLB, err := lb.NewLoadBalancer(ctx, "api-server-lb", &lb.LoadBalancerArgs{
			LoadBalancerType: pulumi.String("network"),
			Subnets:          pulumi.StringArray{subnet.ID()},
			SecurityGroups:   pulumi.StringArray{apiServerLBSG.ID()},
			Internal:         pulumi.Bool(false),
		})
		if err != nil {
			return err
		}

		apiServerTG, err := lb.NewTargetGroup(ctx, "api-server-tg", &lb.TargetGroupArgs{
			Port:       pulumi.Int(6443),
			Protocol:   pulumi.String("TCP"),
			VpcId:      vpc.ID(),
			TargetType: pulumi.String("instance"),
			HealthCheck: &lb.TargetGroupHealthCheckArgs{
				Protocol:           pulumi.String("TCP"),
				Port:               pulumi.String("6443"),
				Interval:           pulumi.Int(30),
				HealthyThreshold:   pulumi.Int(3),
				UnhealthyThreshold: pulumi.Int(3),
			},
		})
		if err != nil {
			return err
		}

		_, err = lb.NewTargetGroupAttachment(ctx, "api-server-tg-attachment", &lb.TargetGroupAttachmentArgs{
			TargetGroupArn: apiServerTG.Arn,
			TargetId:       controlPlane.ID(),
			Port:           pulumi.Int(6443),
		})
		if err != nil {
			return err
		}

		_, err = lb.NewListener(ctx, "api-server-listener", &lb.ListenerArgs{
			LoadBalancerArn: apiServerLB.Arn,
			Port:            pulumi.Int(6443),
			Protocol:        pulumi.String("TCP"),
			DefaultActions: lb.ListenerDefaultActionArray{
				&lb.ListenerDefaultActionArgs{
					Type:           pulumi.String("forward"),
					TargetGroupArn: apiServerTG.Arn,
				},
			},
		})
		if err != nil {
			return err
		}

		workerLaunchTemplate, err := ec2.NewLaunchTemplate(ctx, "worker-launch-template", &ec2.LaunchTemplateArgs{
			Name:         pulumi.String(ctx.Stack() + "-worker-launch-template"),
			ImageId:      pulumi.String(amiId),
			InstanceType: pulumi.String("t3.medium"),

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
		ctx.Export("apiServerLbDns", apiServerLB.DnsName)
		ctx.Export("apiServerLbSgId", apiServerLBSG.ID())

		return nil
	})
}
