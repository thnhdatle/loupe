# Loupe on Kubernetes

Plain manifests plus a `kustomization.yaml`. They are namespace-less: pass `-n <ns>`.

## Apply

```sh
kubectl create namespace loupe
kubectl apply -n loupe -k deploy/k8s
kubectl rollout status -n loupe statefulset/loupe
```

The image is `ghcr.io/thnhdatle/loupe:v0.1.0`. Pin the release you deploy by
editing `images[].newTag` in `kustomization.yaml` (or `kustomize edit set image`).

## Single replica only

Loupe uses an embedded SQLite database guarded by a file lock (single writer).
Keep `replicas: 1`; scaling the StatefulSet up is not supported and the extra pod
will fail to start. The 20Gi `data` volume holds the database and API token.

## Sign in and API token

The image is distroless: it has no shell, `cat` or `tar`, so neither
`kubectl exec ... cat` nor `kubectl cp` can read files in the `loupe` container.

Dashboard: every start logs a single-use login URL, valid for 15 minutes.
Port-forward (below), then open it:

```sh
kubectl logs -n loupe loupe-0 | grep 'dashboard login'
```

The URL looks like `http://localhost:3000/login?code=...`. If it expired or was
used, restart the pod (`kubectl delete pod -n loupe loupe-0`) for a new one.

API token (for `/v1` and `loupe` admin commands): the pod writes it at first
start to `/data/api_token` and never logs it. Read it with an ephemeral debug
container that shares the process namespace and has its own `cat`:

```sh
kubectl debug -n loupe loupe-0 -it --image=busybox:1.36 --target=loupe -- \
  cat /proc/1/root/data/api_token
```

If the debug container is refused access to `/proc/1/root`, add
`--profile=sysadmin`. `kubectl cp` works only if you run a helper pod that mounts
the same PersistentVolumeClaim (`data-loupe-0`) and has `tar`; it cannot copy from
the `loupe` container itself.

## Port-forwarding

```sh
kubectl port-forward -n loupe svc/loupe 3000:3000 4317:4317 4318:4318 9092:9092
```

UI at http://localhost:3000; OTLP gRPC on 4317, OTLP HTTP on 4318, admin on 9092.
In-cluster, send telemetry to `loupe.<ns>.svc:4317` / `:4318`.

## Probes

Probes run `loupe healthcheck` via exec (no shell or curl in the image); it calls
the admin `/readyz` and exits 0 only on HTTP 200.
