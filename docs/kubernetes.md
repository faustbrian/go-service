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
