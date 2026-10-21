# Delivery and NUC rollout

## Publish the reviewed source and image

The initial branch is `feat/registry-mirrors`. Commit author is
`Eric Lu <ericluwj@hotmail.com>`. Review the source, tests and diff first.
The operator publishes the branch and creates the initial default branch/PR
according to repository policy; no IT repository is pushed by the agent.

```sh
git push -u origin feat/registry-mirrors
```

From the clean backend checkout, publish a Linux AMD64 image (requires existing
GHCR login with package-write access; never put credentials in command arguments):

```sh
release_commit=$(git rev-parse HEAD)
release_image="ghcr.io/ericluwj/woodpecker-docker-extended:sha-$release_commit"
docker buildx build --platform linux/amd64 \
  --build-arg VERSION=0.1.0 --build-arg COMMIT="$release_commit" \
  --tag "$release_image" --push --metadata-file /tmp/docker-extended-image.json .
python3 -c 'import json; print("ghcr.io/ericluwj/woodpecker-docker-extended@" + json.load(open("/tmp/docker-extended-image.json"))["containerimage.digest"])'
```

The operator must ensure the agent image is pullable from the NUC. GHCR package
visibility is independent of repository visibility. Use an existing authorized
registry login for a private package; make no unreviewed permission changes.
Pin the returned registry manifest digest, never a local Docker image ID or an
invented digest. Keep the previous Compose file for rollback.

## Prepare the existing Komodo stack

The NUC stack is `sg-nucxi7_woodpecker-agent`. Ordinary resources are UI/database
managed; the restricted IT Komodo repository is now bootstrap-only. Do not
restore old TOMLs or resource-sync declarations.

Set the published image digest and backend to `docker-extended` in the reviewed
NUC Compose candidate, with these explicit mappings:

```json
{"docker.io":"registry-docker-io.pi-xcels.com","registry.k8s.io":"registry-k8s-io.pi-xcels.com","gcr.io":"registry-gcr-io.pi-xcels.com","ghcr.io":"registry-ghcr-io.pi-xcels.com"}
```

Preserve `WOODPECKER_SERVER=192.168.1.111:9000`, hostname `sg-nucxi7`, organization
registration 6, capacity one, Docker socket, Compose project `woodpecker-agent`,
container/service name and volume `sg-nucxi7-woodpecker-agent-data`. Keep the
existing `[[SG_NUCXI7_WOODPECKER_AGENT_SECRET]]` reference and token unchanged.
Do not publish tokens or generated `.komodo.env`.

## Drain, deploy and accept

1. Disable new scheduling on the NUC agent; record the prior backend/labels and
   verify it has no active workflow. Do not cancel work or prune images/volumes.
2. Publish the reviewed IT source via the administrator. If linked repo `it`
   remains authoritative, confirm its configured branch contains the selected
   Compose file. Apply only this stack's reviewed image/backend/mirror change.
3. Through Komodo, deploy only the NUC stack. Confirm registration 6 reconnects
   with `linux/amd64`, backend `docker-extended`, capacity one and the same volume.
4. Before restoring ordinary scheduling, run a scoped acceptance workflow
   selected explicitly for the NUC (use its configured hostname label; verify
   that label in the server before submission). Include one public image from
   each registry and clone/service/step coverage. Set forced pulls for image
   acquisition and correlate timestamped manifest/blob requests in all four NAS
   registry logs. Warm client pulls alone do not establish a cache hit.
5. Verify access to `ghcr.io/pi-xcels/pixcels-runner-image:20260615` separately.
   If anonymous mirror/origin access fails, inspect existing cache/package
   access. Private-origin credentials belong in Woodpecker registry settings,
   causing direct-origin acquisition. Do not forward origin credentials to caches.
6. Confirm completion, workspace continuity, cleanup and no unexpected fallback;
   restore the prior enabled scheduling state. Do not restart pipeline #244 as
   an automatic smoke test; a full application rerun is separate from the scoped
   acceptance workflow.

Root commands on infrastructure hosts remain administrator-run. This rollout
needs no Docker daemon restart, TLS relaxation or socket mount into workflow
containers. Nested Docker/BuildKit builds remain separate.

## Rollback

Disable/drain the NUC, restore the prior pinned/recorded upstream agent image
and `WOODPECKER_BACKEND=docker`, remove the mirror environment variable, and
redeploy only this stack. Preserve registration, token and volume. Verify contact
before restoring scheduling. Retain the custom source/image for diagnosis.
