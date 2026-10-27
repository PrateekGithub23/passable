#!/usr/bin/env bash
# Deploy Passable to a local Kubernetes cluster (kind) and smoke-test it.
#
#   scripts/k8s-up.sh                    # create/update the cluster, deploy, test
#   SKIP_IMPORT=1 scripts/k8s-up.sh      # don't touch the graph (the API needs one to start)
#   REIMPORT=1 scripts/k8s-up.sh         # re-run the OSM import even if the graph is loaded
#   kind delete cluster --name passable  # tear everything down
#
# Needs: docker, kind, kubectl, curl, jq; plus go (and data/ from scripts/fetch_osm.sh) for the import.
# The order mirrors production on Fly: database ready -> migrations -> API.
#   1. infra:   namespace, config, Postgres+PostGIS, Redis, NetworkPolicies
#   2. migrate: a Job running /app/migrate, which must complete before the API starts
#   3. import:  the OSM graph, loaded through a port-forward (skipped if already there)
#   4. api:     2 replicas; then health, route and cross-replica live-update checks
set -euo pipefail
cd "$(dirname "$0")/.."

CLUSTER=passable
NS=passable
IMAGE=passable-api:local
PG_IMAGE=postgis/postgis:16-3.4
REDIS_IMAGE=redis:7-alpine
k() { kubectl --context "kind-$CLUSTER" -n "$NS" "$@"; }
step() { printf '\n==> %s\n' "$*"; }

# Background port-forwards are killed on exit, whether the script succeeds or fails.
PIDS=()
cleanup() { for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done; }
trap cleanup EXIT

# forward <target> <local port> <remote port>: start a port-forward and wait until it accepts connections.
forward() {
  # kubectl directly, not via k(): backgrounding a function forks a subshell, and killing
  # that subshell's PID would leave the kubectl process (and the port) behind.
  kubectl --context "kind-$CLUSTER" -n "$NS" port-forward "$1" "$2:$3" >/dev/null 2>&1 &
  PIDS+=($!)
  for _ in $(seq 1 50); do
    nc -z localhost "$2" 2>/dev/null && return 0
    sleep 0.2
  done
  echo "port-forward to $1 did not come up" >&2
  return 1
}

step "Cluster"
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  echo "kind cluster '$CLUSTER' already exists"
else
  kind create cluster --config deploy/k8s/kind-config.yaml --wait 120s
fi

step "Images"
docker build -q -t "$IMAGE" . >/dev/null
echo "built $IMAGE"
# The PostGIS image is published for amd64 only. Pull that variant explicitly (on Apple
# Silicon it runs under Docker Desktop's emulation), then copy all three images into the
# cluster's node so nothing is pulled from inside the cluster.
NODE_ARCH=linux/$(docker info --format '{{.Architecture}}' | sed 's/x86_64/amd64/;s/aarch64/arm64/')
docker image inspect "$PG_IMAGE" >/dev/null 2>&1 || docker pull -q --platform linux/amd64 "$PG_IMAGE"
docker image inspect "$REDIS_IMAGE" >/dev/null 2>&1 || docker pull -q "$REDIS_IMAGE"
# load <image> <platform>: `kind load docker-image`, falling back to exporting one platform
# only. (Docker Desktop's containerd image store can't export a multi-platform image whose
# other platforms were never downloaded, and `kind load` then fails with "digest not found".)
load() {
  if ! kind load docker-image --name "$CLUSTER" "$1" >/dev/null 2>&1; then
    docker save --platform "$2" "$1" |
      docker exec -i "$CLUSTER-control-plane" ctr -n k8s.io images import --all-platforms --digests --snapshotter=overlayfs - >/dev/null
  fi
  echo "loaded $1 ($2) into the cluster"
}
load "$IMAGE" "$NODE_ARCH"
load "$PG_IMAGE" linux/amd64
load "$REDIS_IMAGE" "$NODE_ARCH"

step "1/4 Infra: Postgres + PostGIS, Redis, NetworkPolicies"
kubectl --context "kind-$CLUSTER" apply -k deploy/k8s/infra
k rollout status statefulset/postgres --timeout=300s
k rollout status deployment/redis --timeout=120s

step "2/4 Migrations (Job)"
k delete job passable-migrate --ignore-not-found >/dev/null # Jobs are immutable; replace it
kubectl --context "kind-$CLUSTER" apply -k deploy/k8s/migrate
if ! k wait --for=condition=complete job/passable-migrate --timeout=180s; then
  k logs job/passable-migrate --tail=50 || true
  exit 1
fi
k logs job/passable-migrate --tail=3

step "3/4 Graph import"
psql_in_cluster() { k exec statefulset/postgres -- psql -U passable -d passable -tAc "$1"; }
edges=$(psql_in_cluster "SELECT count(*) FROM edges")
if [ "${SKIP_IMPORT:-0}" = 1 ]; then
  echo "SKIP_IMPORT=1: leaving the graph as is ($edges edges)"
elif [ "$edges" -gt 0 ] && [ "${REIMPORT:-0}" != 1 ]; then
  echo "graph already imported ($edges edges); REIMPORT=1 to redo it"
elif [ ! -f data/ways.json ]; then
  echo "data/ways.json not found: run scripts/fetch_osm.sh first (or SKIP_IMPORT=1)" >&2
  exit 1
else
  # Port-forward traffic enters the pod through the kubelet, so the Postgres
  # NetworkPolicy (which only admits pods labelled as database clients) doesn't block it.
  forward svc/postgres 15432 5432
  go run ./cmd/importer -db "postgres://passable:passable@localhost:15432/passable"
fi

step "4/4 API (2 replicas)"
existed=$(k get deployment passable-api --ignore-not-found -o name)
kubectl --context "kind-$CLUSTER" apply -k deploy/k8s/api
# The image tag is always :local, so a rebuilt image doesn't change the pod spec: restart
# explicitly to roll the new image out (one pod at a time, per the Deployment's strategy).
[ -n "$existed" ] && k rollout restart deployment/passable-api >/dev/null
k rollout status deployment/passable-api --timeout=180s
# "Rolled out" means the new pods are ready; old ones may still be shutting down. Wait for
# them to go, so the port-forwards below can't attach to a terminating pod.
for _ in $(seq 1 60); do
  [ "$(k get pods -l app=passable-api --no-headers | wc -l)" -le 2 ] && break
  sleep 1
done
k get pods -o wide

step "Smoke test"
forward svc/passable-api 18080 80
health=$(curl -fsS localhost:18080/healthz)
echo "healthz: $health"
edges=$(echo "$health" | jq .edges)

if [ "$edges" -gt 0 ]; then
  # A real route between two campus buildings found by place search: SLC -> Hagey Hall.
  ll() { curl -fsS "localhost:18080/api/v1/places?q=$1" | jq -r '.results[0].point | "\(.[1]),\(.[0])"'; } # [lon,lat] -> lat,lon
  from=$(ll SLC) to=$(ll Hagey)
  curl -fsS "localhost:18080/api/v1/route?from=$from&to=$to&profile=wheelchair" |
    jq -r '"route SLC -> Hagey Hall (wheelchair): \(.distance_m | round) m, A* \(.search_ms) ms, graph version \(.graph_version)"'

  # Live updates across replicas: post hazards to pod A while a WebSocket client listens on
  # pod B. Delivery only works if the event crosses Redis pub/sub between the two pods.
  pods=($(k get pods -l app=passable-api -o jsonpath='{.items[*].metadata.name}'))
  podA=${pods[0]} podB=${pods[1]}
  forward "pod/$podA" 18081 8080
  forward "pod/$podB" 18082 8080
  echo "cross-replica live updates: POST to $podA, WebSocket on $podB"
  go run ./cmd/wsprobe -post http://localhost:18081 -ws ws://localhost:18082/ws -n 4 # 1 warm-up + 4 = the per-session report burst (5)
fi

printf '\nPassable is running in kind cluster "%s".\n' "$CLUSTER"
echo "  API:      kubectl --context kind-$CLUSTER -n $NS port-forward svc/passable-api 8080:80"
echo "  Teardown: kind delete cluster --name $CLUSTER"
