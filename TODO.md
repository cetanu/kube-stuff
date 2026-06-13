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
[ ] Bootstrap Talos by hitting the kube API
[ ] Set up ArgoCD for Kube GitOps
[ ] Use Cilium for the network driver
[ ] Set up a secrets operator
[ ] Probably get AWS CCM set up so that the control-plane can make EBS volumes
[ ] Create Valheim game server pod
[ ] Set up Envoy-Gateway
[ ] Get UDP Routes to access the Valheim game server

