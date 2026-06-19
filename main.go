package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/autoscaling"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/lb"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ssm"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
	"github.com/pulumiverse/pulumi-talos/sdk/go/talos/client"
	"github.com/pulumiverse/pulumi-talos/sdk/go/talos/cluster"
	"github.com/pulumiverse/pulumi-talos/sdk/go/talos/machine"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		// --- CONFIGURATION / PARAMETERS ---
		cfg := config.New(ctx, "")

		keyPairName := cfg.Get("keyPairName")
		if keyPairName == "" {
			keyPairName = "kubeworld-except-it-works-this-time"
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
		zones, err := aws.GetAvailabilityZones(ctx, &aws.GetAvailabilityZonesArgs{
			State: new("available"),
		})
		if err != nil {
			return err
		}

		// Talos Linux AMI
		amiId := "ami-01af9407a2f0b0150"

		// --- IAM ROLE CONFIGURATION ---
		assumeRolePolicy, err := json.Marshal(map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{
				{
					"Effect": "Allow",
					"Principal": map[string]any{
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
				// Inbound Talos API from the Load Balancer Security Group
				&ec2.SecurityGroupIngressArgs{
					Protocol: pulumi.String("tcp"),
					FromPort: pulumi.Int(50000),
					ToPort:   pulumi.Int(50000),
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

		// --- KUBERNETES PRIVATE API SERVER LOAD BALANCER ---
		privateApiServerLB, err := lb.NewLoadBalancer(ctx, "private-api-server-lb", &lb.LoadBalancerArgs{
			LoadBalancerType: pulumi.String("network"),
			Subnets:          pulumi.StringArray{subnet.ID()},
			SecurityGroups:   pulumi.StringArray{apiServerLBSG.ID()},
			Internal:         pulumi.Bool(true),
		})
		if err != nil {
			return err
		}

		// --- TALOS OS CONFIGURATION & SECRETS ---
		// Generate cluster-wide secrets once and persist them securely inside Pulumi state.
		talosSecrets, err := machine.NewSecrets(ctx, "talos-secrets", &machine.SecretsArgs{
			TalosVersion: pulumi.String("v1.13.0"),
		})
		if err != nil {
			return err
		}

		// Generate dynamic machine configurations with resolved Load Balancer DNS endpoint
		controlPlaneConfigResult := machine.GetConfigurationOutput(ctx, machine.GetConfigurationOutputArgs{
			ClusterName:     pulumi.String("kubeworld-cluster"),
			ClusterEndpoint: pulumi.Sprintf("https://%s:6443", privateApiServerLB.DnsName),
			MachineType:     pulumi.String("controlplane"),
			MachineSecrets:  talosSecrets.MachineSecrets,
			ConfigPatches: pulumi.StringArray{
				pulumi.All(apiServerLB.DnsName, privateApiServerLB.DnsName).ApplyT(func(args []any) string {
					pubDns := args[0].(string)
					privDns := args[1].(string)
					return fmt.Sprintf("machine:\n  certSANs:\n    - %s\n    - %s\ncluster:\n  apiServer:\n    certSANs:\n      - %s\n      - %s\n", pubDns, privDns, pubDns, privDns)
				}).(pulumi.StringOutput),
				pulumi.String(`
cluster:
  network:
    cni:
      name: none
  externalCloudProvider:
    enabled: true
machine:
  kubelet:
    registerWithFQDN: true
`),
			},
		})

		workerConfigResult := machine.GetConfigurationOutput(ctx, machine.GetConfigurationOutputArgs{
			ClusterName:     pulumi.String("kubeworld-cluster"),
			ClusterEndpoint: pulumi.Sprintf("https://%s:6443", privateApiServerLB.DnsName),
			MachineType:     pulumi.String("worker"),
			MachineSecrets:  talosSecrets.MachineSecrets,
			ConfigPatches: pulumi.StringArray{
				pulumi.String(`
cluster:
  network:
    cni:
      name: none
  externalCloudProvider:
    enabled: true
machine:
  kubelet:
    registerWithFQDN: true
`),
			},
		})

		workerUserDataBase64 := workerConfigResult.MachineConfiguration().ApplyT(func(s string) string {
			return base64.StdEncoding.EncodeToString([]byte(s))
		}).(pulumi.StringOutput)

		// Create Control Plane Node with UserData
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
			UserData: controlPlaneConfigResult.MachineConfiguration(),
			MetadataOptions: &ec2.InstanceMetadataOptionsArgs{
				HttpPutResponseHopLimit: pulumi.Int(3),
			},
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
			PreserveClientIp: pulumi.String("false"),
		})
		if err != nil {
			return err
		}

		privateApiServerTG, err := lb.NewTargetGroup(ctx, "private-api-server-tg", &lb.TargetGroupArgs{
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
			PreserveClientIp: pulumi.String("false"),
		})
		if err != nil {
			return err
		}

		talosTG, err := lb.NewTargetGroup(ctx, "talos-tg", &lb.TargetGroupArgs{
			Port:       pulumi.Int(50000),
			Protocol:   pulumi.String("TCP"),
			VpcId:      vpc.ID(),
			TargetType: pulumi.String("instance"),
			HealthCheck: &lb.TargetGroupHealthCheckArgs{
				Protocol:           pulumi.String("TCP"),
				Port:               pulumi.String("50000"),
				Interval:           pulumi.Int(30),
				HealthyThreshold:   pulumi.Int(3),
				UnhealthyThreshold: pulumi.Int(3),
			},
			PreserveClientIp: pulumi.String("false"),
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

		_, err = lb.NewTargetGroupAttachment(ctx, "private-api-server-tg-attachment", &lb.TargetGroupAttachmentArgs{
			TargetGroupArn: privateApiServerTG.Arn,
			TargetId:       controlPlane.ID(),
			Port:           pulumi.Int(6443),
		})
		if err != nil {
			return err
		}

		_, err = lb.NewTargetGroupAttachment(ctx, "talos-tg-attachment", &lb.TargetGroupAttachmentArgs{
			TargetGroupArn: talosTG.Arn,
			TargetId:       controlPlane.ID(),
			Port:           pulumi.Int(50000),
		})
		if err != nil {
			return err
		}

		// --- DYNAMIC RUNNER SECURITY GROUP RULES ---
		runnerIP, err := getPublicIP()
		if err != nil {
			return fmt.Errorf("runnerIP is blank and failed to fetch from checkip.amazonaws.com: %w", err)
		}

		runnerLbRule, err := ec2.NewSecurityGroupRule(ctx, "runner-to-lb-api", &ec2.SecurityGroupRuleArgs{
			Type:            pulumi.String("ingress"),
			FromPort:        pulumi.Int(6443),
			ToPort:          pulumi.Int(6443),
			Protocol:        pulumi.String("tcp"),
			CidrBlocks:      pulumi.StringArray{pulumi.String(runnerIP)},
			SecurityGroupId: apiServerLBSG.ID(),
		})
		if err != nil {
			return err
		}

		runnerLbTalosRule, err := ec2.NewSecurityGroupRule(ctx, "runner-to-lb-talos", &ec2.SecurityGroupRuleArgs{
			Type:            pulumi.String("ingress"),
			FromPort:        pulumi.Int(50000),
			ToPort:          pulumi.Int(50000),
			Protocol:        pulumi.String("tcp"),
			CidrBlocks:      pulumi.StringArray{pulumi.String(runnerIP)},
			SecurityGroupId: apiServerLBSG.ID(),
		})
		if err != nil {
			return err
		}

		vpcLbRule, err := ec2.NewSecurityGroupRule(ctx, "vpc-to-lb-api", &ec2.SecurityGroupRuleArgs{
			Type:            pulumi.String("ingress"),
			FromPort:        pulumi.Int(6443),
			ToPort:          pulumi.Int(6443),
			Protocol:        pulumi.String("tcp"),
			CidrBlocks:      pulumi.StringArray{pulumi.String("10.240.0.0/16")},
			SecurityGroupId: apiServerLBSG.ID(),
		})
		if err != nil {
			return err
		}

		vpcLbTalosRule, err := ec2.NewSecurityGroupRule(ctx, "vpc-to-lb-talos", &ec2.SecurityGroupRuleArgs{
			Type:            pulumi.String("ingress"),
			FromPort:        pulumi.Int(50000),
			ToPort:          pulumi.Int(50000),
			Protocol:        pulumi.String("tcp"),
			CidrBlocks:      pulumi.StringArray{pulumi.String("10.240.0.0/16")},
			SecurityGroupId: apiServerLBSG.ID(),
		})
		if err != nil {
			return err
		}

		var bootstrapDeps []pulumi.Resource
		bootstrapDeps = append(bootstrapDeps, controlPlane, runnerLbRule, runnerLbTalosRule, vpcLbRule, vpcLbTalosRule)

		// --- TALOS CLUSTER BOOTSTRAP ---
		bootstrap, err := machine.NewBootstrap(ctx, "talos-bootstrap", &machine.BootstrapArgs{
			Node:                pulumi.String("10.240.0.11"),
			Endpoint:            apiServerLB.DnsName,
			ClientConfiguration: talosSecrets.ClientConfiguration.ToClientConfigurationPtrOutput(),
		}, pulumi.DependsOn(bootstrapDeps), pulumi.ReplaceWith([]pulumi.Resource{controlPlane}))
		if err != nil {
			return err
		}

		// --- RETRIEVE KUBECONFIG ---
		kubeconfig, err := cluster.NewKubeconfig(ctx, "talos-kubeconfig", &cluster.KubeconfigArgs{
			Node:     pulumi.String("10.240.0.11"),
			Endpoint: apiServerLB.DnsName,
			ClientConfiguration: cluster.KubeconfigClientConfigurationArgs{
				CaCertificate:     talosSecrets.ClientConfiguration.CaCertificate(),
				ClientCertificate: talosSecrets.ClientConfiguration.ClientCertificate(),
				ClientKey:         talosSecrets.ClientConfiguration.ClientKey(),
			},
		}, pulumi.DependsOn([]pulumi.Resource{bootstrap}), pulumi.ReplaceWith([]pulumi.Resource{controlPlane}))
		if err != nil {
			return err
		}

		// --- GENERATE TALOSCONFIG ---
		talosconfigResult := client.GetConfigurationOutput(ctx, client.GetConfigurationOutputArgs{
			ClusterName: pulumi.String("kubeworld-cluster"),
			ClientConfiguration: client.GetConfigurationClientConfigurationArgs{
				CaCertificate:     talosSecrets.ClientConfiguration.CaCertificate(),
				ClientCertificate: talosSecrets.ClientConfiguration.ClientCertificate(),
				ClientKey:         talosSecrets.ClientConfiguration.ClientKey(),
			},
			Endpoints: pulumi.StringArray{apiServerLB.DnsName},
			Nodes:     pulumi.StringArray{pulumi.String("10.240.0.11")},
		})

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

		_, err = lb.NewListener(ctx, "talos-listener", &lb.ListenerArgs{
			LoadBalancerArn: apiServerLB.Arn,
			Port:            pulumi.Int(50000),
			Protocol:        pulumi.String("TCP"),
			DefaultActions: lb.ListenerDefaultActionArray{
				&lb.ListenerDefaultActionArgs{
					Type:           pulumi.String("forward"),
					TargetGroupArn: talosTG.Arn,
				},
			},
		})
		if err != nil {
			return err
		}

		_, err = lb.NewListener(ctx, "private-api-server-listener", &lb.ListenerArgs{
			LoadBalancerArn: privateApiServerLB.Arn,
			Port:            pulumi.Int(6443),
			Protocol:        pulumi.String("TCP"),
			DefaultActions: lb.ListenerDefaultActionArray{
				&lb.ListenerDefaultActionArgs{
					Type:           pulumi.String("forward"),
					TargetGroupArn: privateApiServerTG.Arn,
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
			UserData: workerUserDataBase64,
			MetadataOptions: &ec2.LaunchTemplateMetadataOptionsArgs{
				HttpPutResponseHopLimit: pulumi.Int(3),
			},
		}, pulumi.DependsOn([]pulumi.Resource{bootstrap}))
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
		}, pulumi.DependsOn([]pulumi.Resource{bootstrap}))
		if err != nil {
			return err
		}

		// --- EXPORTS ---
		ctx.Export("vpcId", vpc.ID())
		ctx.Export("subnetId", subnet.ID())
		ctx.Export("controlPlanePublicIp", apiServerLB.DnsName)
		ctx.Export("securityGroupId", clusterSG.ID())
		ctx.Export("apiServerLbDns", apiServerLB.DnsName)
		ctx.Export("privateApiServerLbDns", privateApiServerLB.DnsName)
		ctx.Export("apiServerLbSgId", apiServerLBSG.ID())
		// Rewrite the exported kubeconfig so that external clients (like developers and the GitHub runner)
		// connect via the public API server Load Balancer rather than the private one.
		publicKubeconfig := pulumi.All(kubeconfig.KubeconfigRaw, apiServerLB.DnsName, privateApiServerLB.DnsName).ApplyT(func(args []any) string {
			rawKubeconfig := args[0].(string)
			pubDns := args[1].(string)
			privDns := args[2].(string)
			return strings.ReplaceAll(rawKubeconfig, privDns, pubDns)
		}).(pulumi.StringOutput)

		ctx.Export("kubeconfig", publicKubeconfig)
		ctx.Export("talosconfig", talosconfigResult.TalosConfig())

		return nil
	})
}

func getPublicIP() (string, error) {
	resp, err := http.Get("https://checkip.amazonaws.com")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s/32", strings.TrimSpace(string(body))), nil
}
