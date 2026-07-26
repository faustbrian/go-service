# Kubernetes operation

The HTTP API example can run in a pod when `LISTEN_ADDRESS=:8080` is supplied.
The application must mount liveness, startup, and readiness handlers and use
`service.Wait` or `service.Run` so `SIGTERM` changes readiness before graceful
shutdown.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: example
spec:
  replicas: 2
  selector:
    matchLabels:
      app: example
  template:
    metadata:
      labels:
        app: example
    spec:
      terminationGracePeriodSeconds: 45
      containers:
        - name: service
          image: example.invalid/replace-me@sha256:replace-me
          env:
            - name: LISTEN_ADDRESS
              value: ":8080"
          ports:
            - name: http
              containerPort: 8080
          startupProbe:
            httpGet:
              path: /startup
              port: http
            failureThreshold: 30
            periodSeconds: 2
          readinessProbe:
            httpGet:
              path: /ready
              port: http
            periodSeconds: 5
            timeoutSeconds: 2
          livenessProbe:
            httpGet:
              path: /live
              port: http
            periodSeconds: 10
            timeoutSeconds: 2
```

Choose `terminationGracePeriodSeconds` greater than the HTTP shutdown timeout
plus the maximum component cleanup budget. On `SIGTERM`, the service enters
draining before component cleanup; readiness returns 503 and existing requests
may finish. Kubernetes sends `SIGKILL` after the pod grace period, so an
application shutdown bound longer than that period cannot complete reliably.

Do not make liveness depend on databases or queues. A dependency outage should
remove readiness, not trigger a restart loop. Detailed health responses should
remain disabled on externally reachable probe endpoints.
