# Repository Context

This repository (`kube-stuff`) contains the codebase and infrastructure configuration for building a Kubernetes-based self-service PaaS.

---

## Layout

- **[`infra/`](infra/)**:
  Infrastructure provisioning using **Pulumi (Go)**. Bootstraps the AWS VPC, Talos Linux control-plane, and worker nodes.
- **[`k8s/`](k8s/)**:
  GitOps configurations synced by **ArgoCD**.
  - **[`k8s/system/`](k8s/system/)**: ArgoCD integrations (including custom Lua health checks for KubeVela and CloudNativePG in [argocd-cm.yaml](k8s/system/argocd-cm.yaml)).
  - **[`k8s/infra/`](k8s/infra/)**: Custom KubeVela component and trait definitions.
    - [kubevela-traits/](k8s/infra/kubevela-traits/) contains:
      - `sql-database.yaml` (`ComponentDefinition` backed by CloudNativePG).
      - `cron-job.yaml` (`ComponentDefinition` with platform safety constraints).
      - `http-route.yaml` (`TraitDefinition` backed by Envoy Gateway HTTPRoute).
  - **[`k8s/apps/`](k8s/apps/)**: Developer-facing OAM Application configurations. E.g., the `cats` app uses the custom OAM platform catalog definitions.
- **[`todo/`](todo/)**:
  - [progress.md](todo/progress.md): The project roadmap, divided into implementation chunks. Check here first to see what to build next.
  - [vision.md](todo/vision.md): The service catalog design, principles, and developer experience vision.
