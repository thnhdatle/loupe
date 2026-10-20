# Loupe on Kubernetes

Deploy Loupe to Kubernetes. The manifests have no namespace: pass `-n <ns>`.

## Apply

```sh
kubectl create namespace loupe
kubectl apply -n loupe -k deploy/k8s
kubectl rollout status -n loupe statefulset/loupe
```

The image is `ghcr.io/thnhdatle/loupe:v0.1.0`. To pin another release, edit `images[].newTag`
in `kustomization.yaml` (or run `kustomize edit set image`).

## Single replica only

Keep `replicas: 1`. SQLite allows one writer, so an extra pod fails to start.
The 20Gi `data` volume holds the database and the API token.

## Sign in and API token

The image has no shell, cat or tar, so kubectl exec and kubectl cp cannot read its files.

Dashboard: port-forward (below), then open the login URL from the logs.

```sh
kubectl logs -n loupe loupe-0 | grep 'dashboard login'   # http://localhost:3000/login?code=...
kubectl delete pod -n loupe loupe-0                      # new URL if it expired or was used
```

Each start logs a single-use URL, valid for 15 minutes.

API token (for /v1 and loupe admin commands): written to /data/api_token, never logged.
Read it with a debug container:

```sh
kubectl debug -n loupe loupe-0 -it --image=busybox:1.36 --target=loupe -- \
  cat /proc/1/root/data/api_token
```

If access is refused, add `--profile=sysadmin`. Or copy it from a helper pod that mounts the
claim `data-loupe-0` and has tar.

## Port-forwarding

```sh
kubectl port-forward -n loupe svc/loupe 3000:3000 4317:4317 4318:4318 9092:9092
```

UI at http://localhost:3000. OTLP gRPC on 4317, OTLP HTTP on 4318, admin on 9092.
In-cluster, send telemetry to `loupe.<ns>.svc:4317` / `:4318`.

## Probes

Probes exec `loupe healthcheck`: exit 0 only when the admin /readyz returns HTTP 200.
