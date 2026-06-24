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


## Phase 4 - Building the "Heroku" PaaS (2-hour chunks)

### Chunk 1: The Abstraction Layer (KubeVela Basics)
*Goal: Stop writing raw Deployments and Services.*
- [x] Install the KubeVela controller on the cluster.
- [x] Install the `vela` CLI locally.
- [x] Write our first OAM `Application` manifest (deploying a simple web app) instead of raw k8s YAML.
- [ ] Integrate KubeVela into the existing ArgoCD GitOps pipeline (so ArgoCD syncs the `Application` manifest).

### Chunk 2: Magic URLs (Ingress & Routing)
*Goal: Apps automatically get a URL when deployed.*
- [ ] Integrate Envoy-Gateway with KubeVela.
- [ ] Use or write a KubeVela `Trait` that automatically generates Envoy HTTPRoutes/Gateway config.
- [ ] Test deploying an application that automatically provisions a custom subdomain or route.

### Chunk 3: Developer UI & Self-Service
*Goal: Provide a web dashboard so users don't even need to touch git if they don't want to.*
- [ ] Install VelaUX (KubeVela's official web dashboard).
- [ ] Define standard "Components" (e.g., `standard-web-app`, `background-worker`) for users to choose from.
- [ ] Configure basic RBAC so a user can log into the UI and only see/manage their own applications.

### Chunk 4: Secrets & Stateful Services
*Goal: Securely inject credentials and allow apps to have persistent storage.*
- [ ] Set up a Secrets Operator (like External Secrets Operator).
- [ ] Configure KubeVela to securely bind these secrets to the components.
- [ ] Create a KubeVela storage trait to dynamically provision volumes using the existing AWS EBS CSI driver.

### Chunk 5: The "Git Push" Experience (Source-to-Image)
*Goal: Replicate the `git push heroku master` feel.*
- [ ] Install a build engine like `kpack` or set up a Tekton pipeline.
- [ ] Configure a GitHub webhook to trigger a container build upon a push to a repo.
- [ ] Hook the build registry up to KubeVela so it automatically rolls out the new image upon a successful build.

### Chunk 6: Hard Multi-Tenancy & Security
*Goal: Make sure users can't break the cluster or see each other's data.*
- [ ] Implement strict Cilium Network Policies to isolate namespaces (tenant A cannot talk to tenant B).
- [ ] Enforce Kubernetes Pod Security Standards (prevent root containers, prevent host network access).
- [ ] (Optional) Evaluate `vcluster` (Virtual Clusters) for tenants who actually want full `kubectl` access without compromising the host cluster.

## Other Tasks
- [ ] Full code review, because most of it is written by Gemini
- [ ] Cluster-API and how it fits into the picture
