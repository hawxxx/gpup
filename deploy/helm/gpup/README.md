# GPUP Helm example

Build and push the supplied image to your registry. Create a Secret named `gpup-token` containing a `token` key, then install:

```sh
helm upgrade --install gpup deploy/helm/gpup \
  --set image.repository=registry.example/gpup --set image.tag=0.1.0
kubectl port-forward service/gpup 7331:7331
```

Use your existing secret manager or `kubectl create secret generic gpup-token --from-file=token=/secure/token-file`. Keep token files outside this repository. A ClusterIP Service is created; external ingress and TLS remain operator responsibilities.

One replica uses SQLite with a PVC and Recreate updates. `persistence.enabled=false` uses ephemeral storage. `tokenSecret.name/key`, `dcgmURL`, `readOnly`, and `resources` are configurable. The chart does not provision an inference engine, DCGM Exporter, NVIDIA drivers, GPU access, OIDC, or multiuser authorization.

A node collector DaemonSet and distributed aggregation are deferred. Run independent per-node GPUP instances with NVIDIA runtime configuration when local GPU visibility is required; these do not merge into a cluster dashboard.
