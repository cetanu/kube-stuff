# Turning kubernetes into a real platform

> The goal: developers deploy by putting together components like legos
> They never write Deployments, Services, PVCs, or operator CRDs. They don't need to know kube.
> The infra engineer controls what each type renders into. They add new ones if needed. This benefits hundreds of devs instantly.

---

# Component categories

The building blocks that a dev can use to make their app and deploy it on the cluster.

I've selected pieces that I think cover a solid 80-90% of what any dev needs, or would even be aware of.

Of course, the overall goal of this implementation of kube is that the platform engineer can add new ones whenever it's needed.

## Compute

| Type | Description | Backing implementation | Status |
|---|---|---|---|
| `webservice` | HTTP API or web frontend | KubeVela built-in (Deployment + ClusterIP) | ✅ Done |
| `grpc-service` | gRPC backend service | Custom ComponentDefinition (Deployment + ClusterIP with gRPC ports) | Planned |
| `worker` | Background processor (queue consumer, etc.) | KubeVela built-in (Deployment, no Service) | Built-in |
| `cron-job` | Scheduled tasks | Custom ComponentDefinition (CronJob) | Planned |

## Data

| Type | Description | Backing implementation | Status |
|---|---|---|---|
| `sql-database` | Relational database | CloudNativePG operator (postgres) | ✅ Done |
| `kv-store` | Key-value cache/store | Redis Helm chart in-cluster | Planned |
| `document-store` | Document database | MongoDB operator (if needed) | Planned |

## Messaging

| Type | Description | Backing implementation | Status |
|---|---|---|---|
| `message-queue` | Task/job queue | RabbitMQ operator or SQS via Crossplane | Planned |
| `event-stream` | Event streaming | Kafka (Strimzi) or Kinesis via Crossplane | Planned |

## Storage

| Type | Description | Backing implementation | Status |
|---|---|---|---|
| `blob-storage` | Object/file storage | S3 via Crossplane or MinIO in-cluster | Planned |
| `cdn` | Content delivery | CloudFront/CloudFlare in front of blob-storage | Planned |

---

## Traits (cross-cutting, attach to any component)

| Trait | Description | Backing implementation | Status |
|---|---|---|---|
| `http-route` | Expose via HTTP URL | Envoy Gateway + HTTPRoute | ✅ Done |
| `grpc-route` | Expose via gRPC | Envoy Gateway + GRPCRoute | Planned |
| `udp-route` | Expose via UDP | Envoy Gateway + UDPRoute | Planned |
| `scaler` | Replica count / HPA | KubeVela built-in | ✅ Built-in |
| `storage` | Attach persistent volume | PVC with ebs-sc StorageClass | Planned |
| `resource-limits` | CPU/memory guardrails | Container resources | Planned |
| `network-policy` | Tenant isolation | Cilium NetworkPolicy | Planned (Chunk 6) |

---

## Design Principles

1. **Devs pick from the menu** — they never write raw k8s YAML
2. **Infra engineer controls the backing** — ComponentDefinitions and TraitDefinitions decide what actually gets created
3. **Connection wiring is automatic** — data components produce a Secret with well-known keys (HOST, PORT, PASSWORD), consuming components reference by name
4. **Environment overrides via Policies** — same manifest, different backing (e.g. in-cluster postgres for dev, Cloud SQL for prod)
5. **Everything goes through ArgoCD** — git push to deploy, ArgoCD syncs, KubeVela reconciles

---

## Picture of the Developer Experience

A dev can push this manifest:

```yaml
apiVersion: core.oam.dev/v1beta1
kind: Application
metadata:
  name: my-app
  namespace: my-team
spec:
  components:
    - name: api
      type: webservice
      properties:
        image: myrepo/my-api:v1.2
        port: 8080
        env:
          - name: DATABASE_URL
            valueFrom:
              secretKeyRef:
                name: db  # auto-created by sql-database component
                key: url
      traits:
        - type: http-route
        - type: scaler
          properties:
            replicas: 3

    - name: db
      type: sql-database
      properties:
        storage: 10Gi

    - name: cache
      type: kv-store
      properties:
        maxMemory: 256Mi
```

## Can it be even better?

I think yes.

Some ideas:

### Web interface

Let them define the above structure in a web interface. They can click-ops
their way to a deployment, and you can provide them with the YAML file that
they created, so they can manage it as code from that point on. You can provide
an API for power-users.

### CLI + Server combination

Strip the required YAML down even more. Give them a CLI. The CLI sends the YAML
to a server. The server then passes it on to kube.

```yaml
name: my-app
namespace: my-team
components:
- name: api
  type: webservice
  properties:
    image: myrepo/my-api:v1.2
    port: 8080
    env:
      - name: DATABASE_URL
        valueFrom:
          secretKeyRef:
            name: db  # auto-created by sql-database component
            key: url
  traits:
    - type: http-route
    - type: scaler
      properties:
        replicas: 3

- name: db
  type: sql-database
  properties:
    storage: 10Gi

- name: cache
  type: kv-store
  properties:
    maxMemory: 256Mi
```
