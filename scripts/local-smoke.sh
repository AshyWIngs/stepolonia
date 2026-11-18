#!/usr/bin/env bash
# Run a local smoke command with a disposable builder and optional kind cluster.
set -euo pipefail

use_kind=false
if [[ ${1:-} == --kind ]]; then
    use_kind=true
    shift
fi
if [[ ${1:-} != -- || $# -lt 2 ]]; then
    echo "Usage: $0 [--kind] -- command [args ...]" >&2
    exit 2
fi
shift
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

if [[ -n ${DOCKER_HOST:-} && -z ${DOCKER_CONTEXT:-} ]]; then
    echo "Select the local Docker context explicitly instead of DOCKER_HOST" >&2
    exit 2
fi
unset DOCKER_HOST
DOCKER_CONTEXT=$(docker context show)
export DOCKER_CONTEXT
endpoint=$(docker context inspect "$DOCKER_CONTEXT" --format '{{.Endpoints.docker.Host}}')
if [[ $endpoint != unix:///* ]]; then
    echo "Smoke tests require a local Docker Unix socket" >&2
    exit 2
fi

name="sp-smoke-$(python3 -c 'import secrets; print(secrets.token_hex(8))')"
builders=$(docker buildx ls --format '{{.Name}}')
if grep -Fxq -- "$name" <<< "$builders"; then
    echo "Builder already exists: $name" >&2
    exit 1
fi

scratch=$(mktemp -d "${TMPDIR:-/tmp}/$name.XXXXXXXX")
child=
builder_created=false
cluster_attempted=false
network=
cleanup() {
    local status=$? cleanup_status=0 remaining
    trap - EXIT
    trap '' INT TERM
    if [[ -n $child ]] && kill -TERM -- "-$child" 2>/dev/null; then
        for ((remaining = 30; remaining > 0; remaining--)); do
            kill -0 -- "-$child" 2>/dev/null || break
            sleep 1
        done
        kill -KILL -- "-$child" 2>/dev/null || true
        wait "$child" || true
    fi
    if [[ $cluster_attempted == true ]]; then
        kind delete cluster --name "$name" || cleanup_status=1
    fi
    if [[ -n $network ]]; then
        docker network rm "$network" || cleanup_status=1
    fi
    if [[ $builder_created == true ]]; then
        docker buildx rm "$name" || cleanup_status=1
    fi
    rm -rf -- "$scratch" || cleanup_status=1
    if ((status == 0)); then
        status=$cleanup_status
    fi
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
echo "Local smoke run: $name"

docker buildx create --name "$name" --driver docker-container "$DOCKER_CONTEXT"
builder_created=true
export BUILDX_BUILDER="$name"
export STEPOLONIA_SMOKE_TMP="$scratch"

if [[ $use_kind == true ]]; then
    export KUBECONFIG="$scratch/kubeconfig"
    export KIND_EXPERIMENTAL_PROVIDER=docker
    clusters=$(kind get clusters)
    if grep -Fxq -- "$name" <<< "$clusters"; then
        echo "kind cluster already exists: $name" >&2
        exit 1
    fi
    network=$(docker network create --label "io.stepolonia.smoke=$name" "$name")
    export KIND_EXPERIMENTAL_DOCKER_NETWORK="$name"
    export STEPOLONIA_SMOKE_CLUSTER="$name"
    cluster_attempted=true
    kind create cluster --name "$name" --kubeconfig "$KUBECONFIG" --wait 120s
fi

# Give the command its own process group so Ctrl+C and SIGTERM stop its children.
set -m
"$@" &
child=$!
status=0
wait "$child" || status=$?
child=
exit "$status"
