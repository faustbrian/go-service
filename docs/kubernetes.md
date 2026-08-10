# Kubernetes operation

The cohesive runtime is Kubernetes-compatible without importing Kubernetes
packages. Every long-running command owns one management listener with
`/livez`, `/startupz`, and `/readyz`; one-shot commands open no listener unless
explicitly requested.

Set `MANAGEMENT_ADDRESS=0.0.0.0:8081` in pods. The package default remains the
safer `127.0.0.1:8081` for non-Kubernetes processes. Never put the management
port in a public Service or Ingress.

The examples below use one command binary:

```text
/service serve
/service worker
/service schedule
/service migrate
```

Replace the example image with an immutable digest built from the application.

## API or RPC Deployment

An API and an RPC service use the same process contract; only the
application-owned `http.Handler` differs.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: example-api
spec:
  replicas: 2
  selector:
    matchLabels:
      app: example-api
  template:
    metadata:
      labels:
        app: example-api
    spec:
      terminationGracePeriodSeconds: 45
      containers:
        - name: service
          image: example.invalid/service@sha256:replace-me
          command: ["/service", "serve"]
          env:
            - name: LISTEN_ADDRESS
              value: "0.0.0.0:8080"
            - name: MANAGEMENT_ADDRESS
              value: "0.0.0.0:8081"
          ports:
            - name: http
              containerPort: 8080
            - name: management
              containerPort: 8081
          startupProbe:
            httpGet:
              path: /startupz
              port: management
            failureThreshold: 40
            periodSeconds: 1
            timeoutSeconds: 1
          readinessProbe:
            httpGet:
              path: /readyz
              port: management
            failureThreshold: 2
            periodSeconds: 2
            timeoutSeconds: 1
          livenessProbe:
            httpGet:
              path: /livez
              port: management
            failureThreshold: 3
            periodSeconds: 5
            timeoutSeconds: 1
---
apiVersion: v1
kind: Service
metadata:
  name: example-api
spec:
  selector:
    app: example-api
  ports:
    - name: http
      port: 80
      targetPort: http
```

The Service deliberately exposes only the business port. Configure Ingress the
same way.

## Worker Deployment

A worker has no business listener. Kubernetes still consumes the dedicated
management listener because `worker` is long-running.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: example-worker
spec:
  replicas: 2
  selector:
    matchLabels:
      app: example-worker
  template:
    metadata:
      labels:
        app: example-worker
    spec:
      terminationGracePeriodSeconds: 45
      containers:
        - name: service
          image: example.invalid/service@sha256:replace-me
          command: ["/service", "worker"]
          env:
            - name: MANAGEMENT_ADDRESS
              value: "0.0.0.0:8081"
          ports:
            - name: management
              containerPort: 8081
          startupProbe:
            httpGet:
              path: /startupz
              port: management
            failureThreshold: 40
            periodSeconds: 1
            timeoutSeconds: 1
          readinessProbe:
            httpGet:
              path: /readyz
              port: management
            failureThreshold: 2
            periodSeconds: 2
            timeoutSeconds: 1
          livenessProbe:
            httpGet:
              path: /livez
              port: management
            failureThreshold: 3
            periodSeconds: 5
            timeoutSeconds: 1
```

The queue adapter must stop intake before waiting for in-flight work. Queue
acknowledgement, retry, and dead-letter policy remain application-owned.

## Scheduler Deployment

The scheduler role uses the same long-running probe contract. Leader election,
non-overlap, and schedules remain owned by the scheduler module.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: example-scheduler
spec:
  replicas: 1
  selector:
    matchLabels:
      app: example-scheduler
  template:
    metadata:
      labels:
        app: example-scheduler
    spec:
      terminationGracePeriodSeconds: 45
      containers:
        - name: service
          image: example.invalid/service@sha256:replace-me
          command: ["/service", "schedule"]
          env:
            - name: MANAGEMENT_ADDRESS
              value: "0.0.0.0:8081"
          ports:
            - name: management
              containerPort: 8081
          startupProbe:
            httpGet:
              path: /startupz
              port: management
            failureThreshold: 40
            periodSeconds: 1
            timeoutSeconds: 1
          readinessProbe:
            httpGet:
              path: /readyz
              port: management
            failureThreshold: 2
            periodSeconds: 2
            timeoutSeconds: 1
          livenessProbe:
            httpGet:
              path: /livez
              port: management
            failureThreshold: 3
            periodSeconds: 5
            timeoutSeconds: 1
```

## Migration Job

`migrate` is one-shot. Process completion and its exit status are the health
contract, so the Job has no management address, ports, or HTTP probes.

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: example-migration
spec:
  backoffLimit: 1
  activeDeadlineSeconds: 300
  template:
    metadata:
      labels:
        app: example-migration
    spec:
      restartPolicy: Never
      terminationGracePeriodSeconds: 45
      containers:
        - name: service
          image: example.invalid/service@sha256:replace-me
          command: ["/service", "migrate"]
```

## Resilience state and capacity

The owned in-memory breaker, adaptive throttle, concurrency limiter, bulkhead,
semaphore, rate store, and resilience budget are per-pod state. They do not
coordinate exclusion, capacity, attempt budgets, or learning across replicas.
Use an explicitly selected distributed rate backend or coordinator only when
the contract requires cluster-wide state; do not describe a process-local
policy as distributed because Kubernetes schedules it.

Derive per-pod bounds from downstream safe capacity and the Deployment's
maximum replica count, not its current replica count:

```text
usable downstream concurrency = downstream safe concurrency - reserved capacity
per-pod hard admission         = floor(usable downstream concurrency / maxReplicas)
aggregate at minReplicas       = per-pod hard admission * minReplicas
aggregate at maxReplicas       = per-pod hard admission * maxReplicas
```

For example, if a dependency has a reviewed safe concurrency of 120, reserves
20 for maintenance and non-Kubernetes clients, and the HPA range is three to
six pods, the per-pod bulkhead or hard concurrency maximum is
`floor((120 - 20) / 6) = 16`. Aggregate policy capacity is then 48 at three
pods and 96 at six pods. The four remaining usable slots are intentional
rounding headroom. Set a lower initial adaptive limit only from cold-start or
latency evidence. Derive rate, queue, retry, and hedge bounds from the same
downstream throughput, latency, and amplification model; copying the number 16
to unrelated resources is not valid configuration.

Each new pod starts with cold breaker, throttle, limiter, and budget history.
Startup probes must cover construction, but readiness should not wait for
synthetic policy warming. During a mixed-revision rollout, old and new pods
may have different policy revisions and independent histories. Keep names and
outcome semantics compatible, label diagnostics by bounded revision, and size
both revisions so their combined maximum remains within downstream capacity.

HPA signals create feedback loops. Scaling on latency or CPU can add cold pods
while readiness withdrawal or open breakers remove effective capacity. Local
rejections should be observable HPA context, not blindly treated as downstream
errors. Do not wire breaker state directly to readiness or scale solely on a
rejection ratio without checking offered load, ready replicas, and downstream
saturation.

`TestResilienceFleetBoundsOutageAmplificationDuringScalingAndRollout` models
this contract deterministically across minimum and maximum replica counts,
mixed revisions, cold per-pod histories, a sustained backend outage, and HPA
scale-out. It asserts that total physical attempts never exceed the reviewed
max-replica allocation multiplied by the shared per-execution attempt bound.

## Termination contract

On readiness withdrawal, endpoints may remain in load-balancer and client
caches briefly. The application must reject new local admission even if a late
connection reaches the pod. On `SIGTERM`, `service` withdraws readiness,
invokes component `CloseAdmission` once, cancels supervised work, drains
accepted handlers and attempts, and then runs context-bounded policy drain and
observer shutdown in reverse ownership order.

Set `terminationGracePeriodSeconds` greater than the sum of endpoint-removal
delay, the service shutdown deadline, and a measured safety margin. If the
grace period expires, Kubernetes may send `SIGKILL`; active permits, in-memory
budgets, observations, and unsaved work disappear without cleanup. Design
accepted work for idempotent retry or durable acknowledgement and treat cleanup
as best effort under forced termination.

Abrupt pod, node, runtime, or network loss has no readiness-withdrawal or drain
phase. Process-local policy state is lost and cannot release a distributed
lease. Any distributed backend must own TTL, fencing, and recovery semantics.
The replacement pod starts cold and must remain within the max-replica-derived
capacity without relying on the lost pod to close admission.

## Disposable-cluster verification

Run the complete local Kubernetes lifecycle contract with:

```sh
make kubernetes
```

The gate downloads the pinned kind v0.31.0 binary after verifying its embedded
platform checksum, starts Kubernetes v1.35.0 from the pinned node-image digest,
and uses an isolated kubeconfig. It builds Linux scratch images for the
cohesive HTTP process and mixed-role migration command, then proves:

- the Deployment becomes ready without restarts;
- the Service exposes only the business listener;
- all canonical probes implement their GET, HEAD, method, security-header,
  response-schema, and correlation contracts;
- a business request implements the correlation contract;
- readiness changes from `200` to `503` before the management listener becomes
  unavailable while an admitted business request is draining;
- pod deletion completes inside the declared five-second termination grace;
- worker and scheduler Deployments become ready without restarts, expose no
  Service, serve all management probes, and terminate within the same grace;
  and
- the one-shot migration Job exits successfully without ports or probes.

The passing report is written atomically to
`.artifacts/pkg/service/kubernetes/report.json`. It records the execution
revision, complete service and benchmark gate-input digests, tool and cluster
versions, binary and image identities, lifecycle timings, and every assertion.
The ignored artifact is local evidence; hosted or managed-cluster behavior is
not inferred from it.

The gate deletes only its uniquely named cluster and images. When Docker has no
`kind` network, the gate creates a dedicated dual-stack network and removes it
only if the identifier still matches and no containers remain attached. Set
`SERVICE_KUBERNETES_NETWORK_IPV4` and
`SERVICE_KUBERNETES_NETWORK_IPV6` when the documented defaults overlap local
Docker networks. An existing compatible `kind` bridge is reused without being
modified or removed.

## Probe and termination budgets

The startup probe above allows 40 seconds because platform component startup
defaults to a 30-second bound. Tighten it only after measuring the selected
role. The one-second probe timeout is below the management server's independent
five-second read and write bounds, so kubelet abandons a delayed probe first.

The examples allow 45 seconds for termination. The platform withdraws
readiness before it drains business HTTP or task intake, bounds normal service
cleanup to 30 seconds, and shuts the management listener down last. Set
`terminationGracePeriodSeconds` above the largest configured application
shutdown budget; Kubernetes sends `SIGKILL` when that grace period expires.

Do not add external dependencies to liveness. A PostgreSQL, Valkey, Kafka, or
queue outage may affect readiness only when accepting new work would be
incorrect. A transient readiness failure is re-evaluated on the next probe.

## Network exposure

Apply default-deny ingress policy and allow:

- the business port only from the intended ingress or internal callers; and
- port 8081 only from the kubelet/node probe source required by the cluster
  network implementation.

Kubelet source addresses differ between CNI and managed Kubernetes providers,
so the node CIDR or provider-specific selector must be supplied by the
deployment repository. Verify the resulting NetworkPolicy in the target
cluster. Do not create a Service, Ingress, or gateway route for port 8081.

Detailed readiness output, profiling, configuration, and dependency addresses
remain disabled on the management listener.
