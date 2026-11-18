# Disposable local smoke tests

Run local smoke commands in a disposable BuildKit builder. The publishing and resource
test harnesses remove their containers, networks, PostgreSQL volumes, and image tags.
Reports stay in `reports/`.

```sh
./scripts/local-smoke.sh -- make publishing-test
./scripts/local-smoke.sh --kind -- kubectl get nodes
```

The Kubernetes mode requires `kind` and `kubectl`. It creates a temporary kubeconfig with
its own cluster, Docker network, and builder. The launcher removes them after success,
failure, Ctrl+C, or SIGTERM. Docker base images can be reused by subsequent runs.

To load a build into the cluster, export the image to an archive in the temporary
directory. This keeps project image tags out of the host Docker image store.

```sh
./scripts/local-smoke.sh --kind -- sh -eu -c '
  docker buildx build --tag stepolonia-api:smoke \
    --output "type=docker,dest=$STEPOLONIA_SMOKE_TMP/api.tar" apps/api
  kind load image-archive --name "$STEPOLONIA_SMOKE_CLUSTER" "$STEPOLONIA_SMOKE_TMP/api.tar"
'
```
