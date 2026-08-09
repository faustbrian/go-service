#!/usr/bin/env bash
set -euo pipefail

readonly KIND_VERSION="v0.31.0"
readonly KIND_NODE_IMAGE="kindest/node:v1.35.0@sha256:452d707d4862f52530247495d180205e029056831160e22870e37e3f6c1ac31f"
readonly NETWORK_NAME="${SERVICE_KUBERNETES_NETWORK_NAME:-kind}"
readonly NETWORK_IPV4="${SERVICE_KUBERNETES_NETWORK_IPV4:-10.200.0.0/24}"
readonly NETWORK_IPV6="${SERVICE_KUBERNETES_NETWORK_IPV6:-fc00:f853:ccd:e794::/64}"
readonly TERMINATION_GRACE_SECONDS=5

root="$(git rev-parse --show-toplevel)"
service_directory="${root}/pkg/service"
benchmark_directory="${service_directory}/benchmarks/platform"
artifact_directory="${root}/.artifacts/pkg/service/kubernetes"
temporary="$(mktemp -d "${TMPDIR:-/tmp}/service-kubernetes.XXXXXX")"
local_proxy="${temporary}/proxy"
local_modcache="${temporary}/modcache"
cluster_name="service-platform-$$_${RANDOM}"
cluster_name="${cluster_name//_/-}"
kubeconfig="${temporary}/kubeconfig"
kind_binary="${temporary}/kind"
network_created=0
network_id=""
cluster_created=0
background_pids=()
cohesive_image="service-platform-cohesive:${cluster_name}"
migration_image="service-platform-migration:${cluster_name}"
report_temporary=""

cleanup() {
    local status=$?
    local cleanup_failed=0
    local pid current_network_id attachments image
    for pid in "${background_pids[@]:-}"; do
        if kill -0 "${pid}" 2>/dev/null; then
            kill "${pid}" 2>/dev/null || true
        fi
        wait "${pid}" 2>/dev/null || true
    done
    if [[ "${cluster_created}" -eq 1 ]]; then
        if ! "${kind_binary}" delete cluster \
            --name "${cluster_name}" \
            --kubeconfig "${kubeconfig}" >/dev/null 2>&1; then
            cleanup_failed=1
        fi
        if docker container inspect "${cluster_name}-control-plane" \
            >/dev/null 2>&1; then
            cleanup_failed=1
        fi
    fi
    if command -v docker >/dev/null 2>&1; then
        for image in "${cohesive_image}" "${migration_image}"; do
            if docker image inspect "${image}" >/dev/null 2>&1 &&
                ! docker image rm "${image}" >/dev/null 2>&1; then
                cleanup_failed=1
            fi
        done
    fi
    if [[ "${network_created}" -eq 1 ]]; then
        current_network_id="$(
            docker network inspect "${NETWORK_NAME}" --format '{{.Id}}' 2>/dev/null || true
        )"
        attachments="$(
            docker network inspect "${NETWORK_NAME}" \
                --format '{{len .Containers}}' 2>/dev/null || true
        )"
        if [[ "${current_network_id}" == "${network_id}" && "${attachments}" == "0" ]]; then
            if ! docker network rm "${network_id}" >/dev/null 2>&1; then
                cleanup_failed=1
            fi
        elif [[ "${current_network_id}" == "${network_id}" ]]; then
            cleanup_failed=1
        fi
    fi
    if ! chmod -R u+w "${temporary}" 2>/dev/null ||
        ! rm -rf "${temporary}"; then
        cleanup_failed=1
    fi
    trap - EXIT HUP INT TERM
    if [[ "${status}" -eq 0 && "${cleanup_failed}" -eq 0 &&
        -n "${report_temporary}" ]]; then
        mv "${report_temporary}" "${artifact_directory}/report.json"
        report_temporary=""
        printf 'Kubernetes lifecycle evidence passed: %s\n' \
            "${artifact_directory}/report.json"
    elif [[ -n "${report_temporary}" ]]; then
        rm -f "${report_temporary}"
    fi
    if [[ "${status}" -eq 0 && "${cleanup_failed}" -ne 0 ]]; then
        printf 'Kubernetes lifecycle resources were not fully cleaned\n' >&2
        exit 1
    fi
    exit "${status}"
}
trap cleanup EXIT HUP INT TERM

require_command() {
    command -v "$1" >/dev/null 2>&1 || {
        printf 'required command is unavailable: %s\n' "$1" >&2
        exit 1
    }
}

sha256_file() {
    shasum -a 256 "$1" | awk '{print $1}'
}

timestamp_milliseconds() {
    python3 -c 'import time; print(time.time_ns() // 1000000)'
}

kube() {
    kubectl --kubeconfig "${kubeconfig}" "$@"
}

wait_for_file_pattern() {
    local file="$1"
    local pattern="$2"
    local attempts="${3:-200}"
    local attempt
    for ((attempt = 0; attempt < attempts; attempt++)); do
        if [[ -f "${file}" ]] && grep -Eq "${pattern}" "${file}"; then
            return 0
        fi
        sleep 0.05
    done
    printf 'timed out waiting for %s in %s\n' "${pattern}" "${file}" >&2
    [[ -f "${file}" ]] && sed -n '1,80p' "${file}" >&2
    return 1
}

wait_for_port_forward() {
    local log="$1"
    wait_for_file_pattern "${log}" 'Forwarding from 127\.0\.0\.1:[0-9]+ ->'
    sed -nE 's/.*Forwarding from 127\.0\.0\.1:([0-9]+) ->.*/\1/p' \
        "${log}" | head -n 1
}

forget_background_pid() {
    local completed_pid="$1"
    local index
    for index in "${!background_pids[@]}"; do
        if [[ "${background_pids[${index}]}" == "${completed_pid}" ]]; then
            unset 'background_pids[index]'
        fi
    done
}

stop_background_pid() {
    local pid="$1"
    if kill -0 "${pid}" 2>/dev/null; then
        kill "${pid}" 2>/dev/null || true
    fi
    wait "${pid}" 2>/dev/null || true
    forget_background_pid "${pid}"
}

assert_header() {
    local headers="$1"
    local name="$2"
    local expected="$3"
    awk -v name="${name}" -v expected="${expected}" '
        BEGIN { IGNORECASE = 1; found = 0 }
        {
            sub(/\r$/, "")
            split($0, fields, ":")
            if (tolower(fields[1]) == tolower(name)) {
                value = substr($0, index($0, ":") + 1)
                sub(/^[[:space:]]+/, "", value)
                if (value == expected) found = 1
            }
        }
        END { exit found ? 0 : 1 }
    ' "${headers}" || {
        printf 'missing header %s: %s\n' "${name}" "${expected}" >&2
        return 1
    }
}

header_value() {
    local headers="$1"
    local name="$2"
    awk -v name="${name}" '
        BEGIN { IGNORECASE = 1 }
        {
            sub(/\r$/, "")
            split($0, fields, ":")
            if (tolower(fields[1]) == tolower(name)) {
                value = substr($0, index($0, ":") + 1)
                sub(/^[[:space:]]+/, "", value)
                print value
                exit
            }
        }
    ' "${headers}"
}

assert_identity_headers() {
    local headers="$1"
    local correlation request
    correlation="$(header_value "${headers}" X-Correlation-ID)"
    request="$(header_value "${headers}" X-Request-ID)"
    [[ -n "${correlation}" && -n "${request}" && "${correlation}" != "${request}" ]] || {
        printf 'response identity headers are missing or not distinct\n' >&2
        return 1
    }
}

probe_request() {
    local address="$1"
    local path="$2"
    local probe="$3"
    local headers="${temporary}/${probe}.headers"
    local body="${temporary}/${probe}.json"
    local status
    status="$(curl -fsS -D "${headers}" -o "${body}" -w '%{http_code}' \
        "http://${address}${path}")"
    [[ "${status}" == "200" ]]
    assert_header "${headers}" Content-Type 'application/json'
    assert_header "${headers}" Cache-Control 'no-store'
    assert_header "${headers}" X-Content-Type-Options 'nosniff'
    assert_identity_headers "${headers}"
    jq -e --arg probe "${probe}" \
        '.status == "ok" and .probe == $probe and (has("checks") | not)' \
        "${body}" >/dev/null
}

prove_managed_role() {
    local role="$1"
    local deployment="service-platform-${role}"
    local manifest="${temporary}/${role}-deployment.yaml"
    local deployment_json="${temporary}/${role}-deployment.json"
    local pod forward_log forward_pid port started finished
    cat >"${manifest}" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ${deployment}
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ${deployment}
  template:
    metadata:
      labels:
        app: ${deployment}
    spec:
      terminationGracePeriodSeconds: ${TERMINATION_GRACE_SECONDS}
      containers:
        - name: service
          image: ${migration_image}
          imagePullPolicy: Never
          args: ["${role}"]
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
            periodSeconds: 1
            timeoutSeconds: 1
            failureThreshold: 30
          readinessProbe:
            httpGet:
              path: /readyz
              port: management
            periodSeconds: 1
            timeoutSeconds: 1
            failureThreshold: 1
          livenessProbe:
            httpGet:
              path: /livez
              port: management
            periodSeconds: 2
            timeoutSeconds: 1
            failureThreshold: 3
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            runAsUser: 65532
            runAsGroup: 65532
YAML
    kube apply -f "${manifest}" >/dev/null
    kube rollout status "deployment/${deployment}" --timeout=120s >/dev/null
    kube get deployment "${deployment}" -o json >"${deployment_json}"
    jq -e '.status.availableReplicas == 1 and .status.readyReplicas == 1' \
        "${deployment_json}" >/dev/null
    kube get service -l "app=${deployment}" -o json |
        jq -e '.items | length == 0' >/dev/null
    pod="$(
        kube get pods -l "app=${deployment}" \
            -o jsonpath='{.items[0].metadata.name}'
    )"
    [[ "$(
        kube get pod "${pod}" -o json |
            jq '[.status.containerStatuses[].restartCount] | add'
    )" == "0" ]]
    forward_log="${temporary}/${role}-forward.log"
    kube port-forward --address 127.0.0.1 "pod/${pod}" :8081 \
        >"${forward_log}" 2>&1 &
    forward_pid=$!
    background_pids+=("${forward_pid}")
    port="$(wait_for_port_forward "${forward_log}")"
    probe_request "127.0.0.1:${port}" /livez liveness
    probe_request "127.0.0.1:${port}" /startupz startup
    probe_request "127.0.0.1:${port}" /readyz readiness
    started="$(timestamp_milliseconds)"
    kube scale "deployment/${deployment}" --replicas=0 >/dev/null
    kube wait --for=delete "pod/${pod}" --timeout=10s >/dev/null
    finished="$(timestamp_milliseconds)"
    last_role_termination_milliseconds=$((finished - started))
    [[ "${last_role_termination_milliseconds}" \
        -le $((TERMINATION_GRACE_SECONDS * 1000)) ]]
    stop_background_pid "${forward_pid}"
    kube delete "deployment/${deployment}" --wait=true >/dev/null
}

for command in curl docker git go jq kubectl python3 shasum; do
    require_command "${command}"
done
docker info >/dev/null

"${root}/scripts/build-local-proxy.sh" \
    "${local_proxy}" v0.0.0 pkg/service/benchmarks/platform
mkdir -p "${local_modcache}"
upstream_proxy="${GOLIB_UPSTREAM_GOPROXY:-$(go env GOPROXY)}"
no_sum_db="$(go env GONOSUMDB)"
export GOPROXY="file://${local_proxy},${upstream_proxy}"
export GONOSUMDB="github.com/faustbrian/golib/*${no_sum_db:+,${no_sum_db}}"
export GOMODCACHE="${local_modcache}"
export GOWORK=off

case "$(uname -s)-$(uname -m)" in
    Darwin-x86_64)
        kind_target="darwin-amd64"
        kind_sha256="a8b3cf77b2ad77aec5bf710d1a2589d9117576132af812885cad41e9dede4d4e"
        ;;
    Darwin-arm64)
        kind_target="darwin-arm64"
        kind_sha256="88bf554fe9da6311c9f8c2d082613c002911a476f6b5090e9420b35d84e70c5c"
        ;;
    Linux-x86_64)
        kind_target="linux-amd64"
        kind_sha256="eb244cbafcc157dff60cf68693c14c9a75c4e6e6fedaf9cd71c58117cb93e3fa"
        ;;
    Linux-aarch64|Linux-arm64)
        kind_target="linux-arm64"
        kind_sha256="8e1014e87c34901cc422a1445866835d1e666f2a61301c27e722bdeab5a1f7e4"
        ;;
    *)
        printf 'unsupported kind host: %s-%s\n' "$(uname -s)" "$(uname -m)" >&2
        exit 1
        ;;
esac
curl -fsSL \
    "https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-${kind_target}" \
    -o "${kind_binary}"
[[ "$(sha256_file "${kind_binary}")" == "${kind_sha256}" ]] || {
    printf 'kind checksum mismatch for %s\n' "${kind_target}" >&2
    exit 1
}
chmod 0700 "${kind_binary}"

if docker network inspect "${NETWORK_NAME}" >/dev/null 2>&1; then
    [[ "$(docker network inspect "${NETWORK_NAME}" --format '{{.Driver}}')" == "bridge" ]] || {
        printf 'existing Docker network is not a bridge: %s\n' "${NETWORK_NAME}" >&2
        exit 1
    }
    docker network inspect "${NETWORK_NAME}" | jq -e '
        .[0].EnableIPv6 == true and
        any(.[0].IPAM.Config[]; .Subnet | contains(":")) and
        any(.[0].IPAM.Config[]; .Subnet | contains("."))
    ' >/dev/null || {
        printf 'existing Docker network is not dual-stack: %s\n' \
            "${NETWORK_NAME}" >&2
        exit 1
    }
    network_id="$(docker network inspect "${NETWORK_NAME}" --format '{{.Id}}')"
else
    network_id="$(docker network create \
        --driver bridge \
        --ipv6 \
        --subnet "${NETWORK_IPV4}" \
        --subnet "${NETWORK_IPV6}" \
        "${NETWORK_NAME}")"
    network_created=1
fi

cat >"${temporary}/kind.yaml" <<'YAML'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  ipFamily: dual
nodes:
  - role: control-plane
YAML

cluster_created=1
KIND_EXPERIMENTAL_DOCKER_NETWORK="${NETWORK_NAME}" \
    "${kind_binary}" create cluster \
        --name "${cluster_name}" \
        --image "${KIND_NODE_IMAGE}" \
        --config "${temporary}/kind.yaml" \
        --kubeconfig "${kubeconfig}" \
        --wait 120s
node_name="${cluster_name}-control-plane"

docker_architecture="$(docker version --format '{{.Server.Arch}}')"
case "${docker_architecture}" in
    amd64|x86_64) go_architecture=amd64 ;;
    arm64|aarch64) go_architecture=arm64 ;;
    *)
        printf 'unsupported Docker architecture: %s\n' "${docker_architecture}" >&2
        exit 1
        ;;
esac

(
    cd "${benchmark_directory}"
    CGO_ENABLED=0 GOOS=linux GOARCH="${go_architecture}" \
        go build -trimpath -ldflags='-s -w' -tags=benchmark_disabled \
            -o "${temporary}/cohesive" ./cmd/cohesive
)
(
    cd "${service_directory}"
    CGO_ENABLED=0 GOOS=linux GOARCH="${go_architecture}" \
        go build -trimpath -ldflags='-s -w' \
            -o "${temporary}/mixed-role" ./examples/mixed-role
)

cat >"${temporary}/Dockerfile.cohesive" <<'DOCKERFILE'
FROM scratch
COPY cohesive /service
USER 65532:65532
ENTRYPOINT ["/service"]
DOCKERFILE
cat >"${temporary}/Dockerfile.migration" <<'DOCKERFILE'
FROM scratch
COPY mixed-role /service
USER 65532:65532
ENTRYPOINT ["/service"]
DOCKERFILE
docker build --quiet -f "${temporary}/Dockerfile.cohesive" \
    -t "${cohesive_image}" "${temporary}" >/dev/null
docker build --quiet -f "${temporary}/Dockerfile.migration" \
    -t "${migration_image}" "${temporary}" >/dev/null
"${kind_binary}" load docker-image --name "${cluster_name}" "${cohesive_image}"
"${kind_binary}" load docker-image --name "${cluster_name}" "${migration_image}"

cat >"${temporary}/deployment.yaml" <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: service-platform
spec:
  replicas: 1
  selector:
    matchLabels:
      app: service-platform
  template:
    metadata:
      labels:
        app: service-platform
    spec:
      terminationGracePeriodSeconds: ${TERMINATION_GRACE_SECONDS}
      containers:
        - name: service
          image: ${cohesive_image}
          imagePullPolicy: Never
          args: ["serve"]
          env:
            - name: BENCH_BUSINESS_ADDRESS
              value: "0.0.0.0:8080"
            - name: BENCH_MANAGEMENT_ADDRESS
              value: "0.0.0.0:8081"
          ports:
            - name: business
              containerPort: 8080
            - name: management
              containerPort: 8081
          startupProbe:
            httpGet:
              path: /startupz
              port: management
            periodSeconds: 1
            timeoutSeconds: 1
            failureThreshold: 30
          readinessProbe:
            httpGet:
              path: /readyz
              port: management
            periodSeconds: 1
            timeoutSeconds: 1
            failureThreshold: 1
          livenessProbe:
            httpGet:
              path: /livez
              port: management
            periodSeconds: 2
            timeoutSeconds: 1
            failureThreshold: 3
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            runAsUser: 65532
            runAsGroup: 65532
---
apiVersion: v1
kind: Service
metadata:
  name: service-platform
spec:
  selector:
    app: service-platform
  ports:
    - name: business
      port: 8080
      targetPort: business
YAML
kube apply -f "${temporary}/deployment.yaml" >/dev/null
kube rollout status deployment/service-platform --timeout=120s >/dev/null

deployment_json="${temporary}/deployment.json"
service_json="${temporary}/service.json"
kube get deployment service-platform -o json >"${deployment_json}"
kube get service service-platform -o json >"${service_json}"
jq -e '.status.availableReplicas == 1 and .status.readyReplicas == 1' \
    "${deployment_json}" >/dev/null
jq -e '
    (.spec.ports | length) == 1 and
    .spec.ports[0].port == 8080 and
    .spec.ports[0].targetPort == "business"
' "${service_json}" >/dev/null

pod="$(kube get pods -l app=service-platform -o jsonpath='{.items[0].metadata.name}')"
pod_ip="$(kube get pod "${pod}" -o jsonpath='{.status.podIP}')"
[[ -n "${pod}" && -n "${pod_ip}" ]]
[[ "$(kube get pod "${pod}" -o json | jq '[.status.containerStatuses[].restartCount] | add')" == "0" ]]

management_forward_log="${temporary}/management-forward.log"
kube port-forward --address 127.0.0.1 "pod/${pod}" :8081 \
    >"${management_forward_log}" 2>&1 &
management_forward_pid=$!
background_pids+=("${management_forward_pid}")
management_port="$(wait_for_port_forward "${management_forward_log}")"
management_address="127.0.0.1:${management_port}"

probe_request "${management_address}" /livez liveness
probe_request "${management_address}" /startupz startup
probe_request "${management_address}" /readyz readiness

head_headers="${temporary}/head.headers"
python3 - "${management_address}" >"${head_headers}" <<'PYTHON'
import socket
import sys

host, port = sys.argv[1].rsplit(":", 1)
with socket.create_connection((host, int(port)), timeout=2) as connection:
    connection.settimeout(2)
    connection.sendall(
        b"HEAD /readyz HTTP/1.1\r\n"
        b"Host: management\r\n"
        b"Connection: close\r\n\r\n"
    )
    chunks = []
    while True:
        chunk = connection.recv(4096)
        if not chunk:
            break
        chunks.append(chunk)
response = b"".join(chunks)
headers, separator, body = response.partition(b"\r\n\r\n")
if separator == b"" or body != b"":
    raise SystemExit("HEAD response contained a body or no header terminator")
if not headers.startswith(b"HTTP/1.1 200 "):
    raise SystemExit("HEAD response status was not 200")
sys.stdout.buffer.write(headers + b"\r\n")
PYTHON
assert_header "${head_headers}" Content-Type 'application/json'
assert_header "${head_headers}" Cache-Control 'no-store'
assert_header "${head_headers}" X-Content-Type-Options 'nosniff'
assert_identity_headers "${head_headers}"

method_headers="${temporary}/method.headers"
method_body="${temporary}/method.body"
method_status="$(curl -sS -X POST -D "${method_headers}" -o "${method_body}" \
    -w '%{http_code}' "http://${management_address}/readyz")"
[[ "${method_status}" == "405" ]]
assert_header "${method_headers}" Allow 'GET, HEAD'
assert_header "${method_headers}" Content-Type 'application/json'
assert_header "${method_headers}" Cache-Control 'no-store'
assert_header "${method_headers}" X-Content-Type-Options 'nosniff'
assert_identity_headers "${method_headers}"

business_forward_log="${temporary}/business-forward.log"
kube port-forward --address 127.0.0.1 service/service-platform :8080 \
    >"${business_forward_log}" 2>&1 &
business_forward_pid=$!
background_pids+=("${business_forward_pid}")
business_port="$(wait_for_port_forward "${business_forward_log}")"
business_headers="${temporary}/business.headers"
business_body="${temporary}/business.json"
business_status="$(curl -fsS -X POST \
    -H 'Content-Type: application/json' \
    -d '{"jsonrpc":"2.0","method":"postal.search","params":{"query":"00100"}}' \
    -D "${business_headers}" -o "${business_body}" -w '%{http_code}' \
    "http://127.0.0.1:${business_port}/postal/search")"
[[ "${business_status}" == "200" ]]
assert_identity_headers "${business_headers}"
jq -e '
    .jsonrpc == "2.0" and
    .result == ["00100", "00101", "00102"]
' "${business_body}" >/dev/null

docker exec "${node_name}" sh -c 'command -v curl >/dev/null && command -v bash >/dev/null'
transition_log="${temporary}/readiness-transition.txt"
docker exec "${node_name}" bash -c '
    for _ in $(seq 1 1200); do
        status=$(curl -sS -o /dev/null -w "%{http_code}" \
            --connect-timeout 0.05 --max-time 0.10 \
            "http://$1:8081/readyz" 2>/dev/null || true)
        if [[ -z "${status}" ]]; then status=000; fi
        printf "%s\n" "${status}"
        sleep 0.005
    done
' -- "${pod_ip}" >"${transition_log}" 2>/dev/null &
transition_pid=$!
background_pids+=("${transition_pid}")
wait_for_file_pattern "${transition_log}" '^200$'

held_log="${temporary}/held-request.log"
docker exec "${node_name}" bash -c '
    exec 3<>"/dev/tcp/$1/8080"
    printf "POST /postal/search HTTP/1.1\r\nHost: service\r\nContent-Type: application/json\r\nContent-Length: 512\r\nExpect: 100-continue\r\nConnection: close\r\n\r\n" >&3
    IFS= read -r response <&3
    [[ "${response}" == "HTTP/1.1 100 Continue"* ]] || exit 1
    printf "held\n" >&2
    cat <&3
' -- "${pod_ip}" >"${temporary}/held-response.txt" 2>"${held_log}" &
held_pid=$!
background_pids+=("${held_pid}")
wait_for_file_pattern "${held_log}" '^held$'

termination_started="$(timestamp_milliseconds)"
kube scale deployment/service-platform --replicas=0 >/dev/null
kube wait --for=delete "pod/${pod}" --timeout=10s >/dev/null
termination_finished="$(timestamp_milliseconds)"
termination_milliseconds=$((termination_finished - termination_started))
[[ "${termination_milliseconds}" -le $((TERMINATION_GRACE_SECONDS * 1000)) ]]
wait "${transition_pid}" || true
forget_background_pid "${transition_pid}"
wait "${held_pid}" 2>/dev/null || true
forget_background_pid "${held_pid}"

transition_sequence="$(awk '
    BEGIN { stage = 0 }
    $0 == "200" && stage == 0 { stage = 1; next }
    $0 == "503" && stage == 1 { stage = 2; next }
    $0 == "000" && stage == 2 { stage = 3; exit }
    END { if (stage == 3) print "200,503,000" }
' "${transition_log}")"
[[ "${transition_sequence}" == "200,503,000" ]] || {
    printf 'readiness transition did not contain 200 -> 503 -> 000\n' >&2
    awk '!seen[$0]++ { print }' "${transition_log}" >&2
    exit 1
}

stop_background_pid "${management_forward_pid}"
stop_background_pid "${business_forward_pid}"
kube delete deployment/service-platform service/service-platform \
    --wait=true >/dev/null

prove_managed_role worker
worker_termination_milliseconds="${last_role_termination_milliseconds}"
prove_managed_role schedule
scheduler_termination_milliseconds="${last_role_termination_milliseconds}"

cat >"${temporary}/migration-job.yaml" <<YAML
apiVersion: batch/v1
kind: Job
metadata:
  name: service-platform-migrate
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: migrate
          image: ${migration_image}
          imagePullPolicy: Never
          args: ["migrate"]
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: ["ALL"]
            readOnlyRootFilesystem: true
            runAsNonRoot: true
            runAsUser: 65532
            runAsGroup: 65532
YAML
kube apply -f "${temporary}/migration-job.yaml" >/dev/null
kube wait --for=condition=complete job/service-platform-migrate --timeout=60s >/dev/null
job_json="${temporary}/job.json"
kube get job service-platform-migrate -o json >"${job_json}"
jq -e '
    .status.succeeded == 1 and
    ((.status.failed // 0) == 0) and
    (.spec.template.spec.containers | length) == 1 and
    (.spec.template.spec.containers[0] | has("ports") | not) and
    (.spec.template.spec.containers[0] | has("startupProbe") | not) and
    (.spec.template.spec.containers[0] | has("readinessProbe") | not) and
    (.spec.template.spec.containers[0] | has("livenessProbe") | not)
' "${job_json}" >/dev/null
job_pod="$(kube get pods -l job-name=service-platform-migrate \
    -o jsonpath='{.items[0].metadata.name}')"
job_pod_json="${temporary}/job-pod.json"
kube get pod "${job_pod}" -o json >"${job_pod_json}"
jq -e '
    .status.phase == "Succeeded" and
    (.status.containerStatuses | length) == 1 and
    .status.containerStatuses[0].restartCount == 0 and
    .status.containerStatuses[0].state.terminated.exitCode == 0
' "${job_pod_json}" >/dev/null

service_digest="$(
    "${root}/scripts/gate-input-digest.sh" kubernetes pkg/service
)"
benchmark_digest="$(
    "${root}/scripts/gate-input-digest.sh" benchmark pkg/service/benchmarks/platform
)"
input_digest="$(
    printf '%s\n' \
        "service=${service_digest}" \
        "benchmark=${benchmark_digest}" \
        "kind=${KIND_VERSION}" \
        "node=${KIND_NODE_IMAGE}" |
        shasum -a 256 | awk '{print $1}'
)"
source_revision="$(git -C "${root}" rev-parse HEAD)"
kind_runtime_version="$("${kind_binary}" version -q)"
kubernetes_version="$(kube version -o json | jq -r '.serverVersion.gitVersion')"
docker_version="$(docker version --format '{{.Server.Version}}')"
cohesive_binary_sha256="$(sha256_file "${temporary}/cohesive")"
migration_binary_sha256="$(sha256_file "${temporary}/mixed-role")"
script_sha256="$(sha256_file "${service_directory}/scripts/check-kubernetes.sh")"
cohesive_image_id="$(docker image inspect "${cohesive_image}" --format '{{.Id}}')"
migration_image_id="$(docker image inspect "${migration_image}" --format '{{.Id}}')"
completed_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"

mkdir -p "${artifact_directory}"
report_temporary="$(mktemp "${artifact_directory}/.report.XXXXXX")"
jq -n \
    --arg schema 'service-kubernetes-lifecycle/v1' \
    --arg result passed \
    --arg source_revision "${source_revision}" \
    --arg input_digest "${input_digest}" \
    --arg service_digest "${service_digest}" \
    --arg benchmark_digest "${benchmark_digest}" \
    --arg completed_at "${completed_at}" \
    --arg host "$(uname -a)" \
    --arg go_version "$(go version)" \
    --arg docker_version "${docker_version}" \
    --arg docker_architecture "${docker_architecture}" \
    --arg kind_version "${kind_runtime_version}" \
    --arg kind_sha256 "${kind_sha256}" \
    --arg node_image "${KIND_NODE_IMAGE}" \
    --arg kubernetes_version "${kubernetes_version}" \
    --arg network_name "${NETWORK_NAME}" \
    --arg network_id "${network_id}" \
    --arg cohesive_binary_sha256 "${cohesive_binary_sha256}" \
    --arg migration_binary_sha256 "${migration_binary_sha256}" \
    --arg script_sha256 "${script_sha256}" \
    --arg cohesive_image_id "${cohesive_image_id}" \
    --arg migration_image_id "${migration_image_id}" \
    --arg transition "${transition_sequence}" \
    --argjson network_created "${network_created}" \
    --argjson termination_grace_seconds "${TERMINATION_GRACE_SECONDS}" \
    --argjson termination_milliseconds "${termination_milliseconds}" \
    --argjson worker_termination_milliseconds \
        "${worker_termination_milliseconds}" \
    --argjson scheduler_termination_milliseconds \
        "${scheduler_termination_milliseconds}" '
    {
      schema: $schema,
      result: $result,
      source_revision: $source_revision,
      input_digest: $input_digest,
      component_input_digests: {
        service: $service_digest,
        benchmark: $benchmark_digest
      },
      completed_at: $completed_at,
      environment: {
        host: $host,
        go: $go_version,
        docker: $docker_version,
        docker_architecture: $docker_architecture,
        kind: $kind_version,
        kind_binary_sha256: $kind_sha256,
        node_image: $node_image,
        kubernetes: $kubernetes_version,
        network: {
          name: $network_name,
          id: $network_id,
          created_for_run: ($network_created == 1)
        }
      },
      artifacts: {
        script_sha256: $script_sha256,
        cohesive_binary_sha256: $cohesive_binary_sha256,
        migration_binary_sha256: $migration_binary_sha256,
        cohesive_image_id: $cohesive_image_id,
        migration_image_id: $migration_image_id
      },
      lifecycle: {
        readiness_transition: $transition,
        termination_grace_seconds: $termination_grace_seconds,
        termination_milliseconds: $termination_milliseconds,
        worker_termination_milliseconds: $worker_termination_milliseconds,
        scheduler_termination_milliseconds: $scheduler_termination_milliseconds
      },
      assertions: {
        deployment_ready_without_restarts: true,
        service_exposes_business_port_only: true,
        canonical_probe_wire_contract: true,
        business_correlation_contract: true,
        readiness_withdrawn_before_listener_close: true,
        termination_within_grace: true,
        worker_management_lifecycle: true,
        scheduler_management_lifecycle: true,
        one_shot_job_completed_without_management_server: true
      }
    }
' >"${report_temporary}"
chmod 0600 "${report_temporary}"
