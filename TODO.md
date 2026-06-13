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
[ ] Use Talos Linux for the cluster OS
[ ] Use Pulumi to define the infra
[ ] Set up ArgoCD for Kube GitOps
[ ] Use Cilium for the network driver
[ ] Set up a secrets operator
[ ] Delete the existing salt/cloudformation code

