# What I've learnt / done so far

## Phase 1
* Kubernetes the Hard Way - Didn't like it
* Manually created EC2
* Manually doing PKI

## Phase 2
* kubeadm init/join
* Cloudformation to create EC2
* saltstack to bootstrap the EC2
* Github Actions to deploy kubeconfigs via OIDC
* Helm to install charts on the controlplane

## Phase 3 - Today's Stream
If we can do it all:
[x] Use Pulumi to define the infra
[x] Use Talos Linux for the cluster OS
[x] Bootstrap Talos by hitting the kube API
[x] Set up ArgoCD for Kube GitOps
[x] Use Cilium for the network driver
[ ] Set up a secrets operator
[x] Probably get AWS CCM set up so that the control-plane can make EBS volumes
[x] Create Valheim game server pod
[x] Set up Envoy-Gateway
[x] Get UDP Routes to access the Valheim game server


## Problems solved
* Destroyed the whole stack and brought it back up
* CCM was being provisioned by Argo, leading to some race condition where it doesn't set a provider id
* Valheim took about 15 minutes to actually boot up


## What's next

* Look at `kubespray`
* Cluster-API and how it fits into the picture
* Full code review, because most of it is written by Gemini
* Think about how to turn the big YAML mess into an actual self-service platform
    * abstracting away all the yaml details somehow, maybe into a webapp
