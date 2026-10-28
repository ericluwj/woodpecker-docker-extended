# Delivery and NUC rollout

## Preferred NUC delivery: Compose source build

The administrator selected keeping Compose and building the agent image locally
from source, instead of depending on a prebuilt GHCR package. The published and
verified source pin is `91d1a0f1334ad5526807303b2187b8c840f3e7dd`.

Use the Dockerfile as a remote Git build context at that full SHA, target
`linux/amd64`, and set build args `VERSION=0.1.0` and `COMMIT` to the same SHA.
Omit `image:`: Compose uses a local project/service name and skips registry pulls
for the agent. Preserve existing runtime environment, identity volume and socket.
The NUC needs Docker/BuildKit and network access for source/base images/modules;
it does not need a host Go toolchain or GHCR package credentials.

On Komodo, set this stack's `run_build=true`, `auto_pull=false` and preserve
`destroy_before_deploy=false`. Its build stage runs before container replacement
and aborts deployment on build failure. Keep project name `woodpecker-agent`.
For a build-only preflight, the administrator can run from the stack directory:

```sh
docker compose --env-file .komodo.env -p woodpecker-agent \
  -f docker-compose.extended.yaml build woodpecker-agent
```

Root execution on the NUC remains administrator-run. After source publication
and drain, deploy this one stack through Komodo with the build stage enabled.
The local validation of this exact remote-context candidate passed, including
AMD64 executable startup; `compose pull` skipped it with `No image to be pulled`.
This is not evidence that the NUC has built or deployed it yet.

## Optional prebuilt image distribution

The initial branch is `main`. Commit author is
`Eric Lu <ericluwj@hotmail.com>`. Review the source, tests and diff first.
The operator publishes `main` as the initial default branch; no IT repository
is pushed by the agent.

```sh
git push -u origin main
```

The manual `Publish agent image` Actions workflow verifies the source, runs
Docker integration tests and checks image startup on native AMD64 and ARM64
runners before publishing a multi-platform image to Docker Hub and GHCR. Follow
the [Docker Hub setup instructions](../README.md#public-prebuilt-images) to
create the public repository and configure `DOCKERHUB_USERNAME` and
`DOCKERHUB_TOKEN`. GHCR still uses the repository's `GITHUB_TOKEN` and requires
package-write permission. Its visibility is managed separately.

Make the workflow available on the default branch through the administrator's
normal publication/review process, then dispatch it for the reviewed commit.
Both registries receive `sha-<full-commit-SHA>`; only runs on `main` also update
`latest`. The run summary records both digest references. Pin the selected
registry's digest for deployment. Publication across registries is not atomic;
check both registries after a failed run before retrying the same commit.

Alternatively, from the clean backend checkout, publish a Linux AMD64 image (requires existing
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

Use the source-build candidate above and backend `docker-extended`, with these
explicit mappings:

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
   Compose file. Apply only this stack's reviewed build/backend/mirror change.
3. Through Komodo, deploy only the NUC stack with `run_build=true`. Confirm registration 6 reconnects
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

## Implementation verification — 2026-10-21

- Race-enabled unit/wrapper tests, `go vet`, native binary build and agent CLI
  smoke test passed; focused statement coverage was 90.1%.
- Local Docker integration passed healthy-mirror and broken-mirror fallback,
  clone/service/step execution, shared workspace, selected image and cleanup.
  No test containers, network or volume remained.
- Direct public-image pulls through all four NAS caches passed from local
  ARM64 Docker. This is not NUC AMD64 or NAS-log acceptance.
- A Linux AMD64 deployment image built successfully and its version command ran
  under local emulation. GHCR publication was denied by existing Docker
  credentials; the existing GitHub CLI token lacks package-write scope.
- Anonymous direct access to the application's AMD64 runner image was denied.
  Its full anonymous AMD64 NAS-cache pull passed with digest
  `sha256:856a08c5993994ce20d3e6233f5b45193428f623b7f9697caa2255e72cd61656`.
  This establishes a usable cache path locally, not NUC or server acceptance.
- NUC non-root SSH by hostname failed DNS resolution; its documented LAN address
  timed out. No root command, Git push, agent restart or live deployment occurred.

The optional release workflow completed successfully on source `91d1a0f`,
publishing `ghcr.io/ericluwj/woodpecker-docker-extended` at registry digest
`sha256:1483ef206c9871fd50d10f9cc7822a2ef4ce57283eb85c4c2521caa98bb058a6`.
An anonymous manifest request was denied; package visibility was not changed.
The selected NUC source-build path avoids that package entirely. NUC deployment,
server-scheduled acceptance and NAS cache-log correlation remain pending.
