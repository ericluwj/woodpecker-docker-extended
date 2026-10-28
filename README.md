# Woodpecker Docker Extended

A standalone Woodpecker v3.19.0 agent with backend `docker-extended`. It wraps
upstream Docker container execution, networks, workspaces, logs and cleanup,
adding registry mirror acquisition for clone, service and step images.

## Public prebuilt images

The manual **Publish agent image** GitHub Actions workflow publishes one
multi-platform image for `linux/amd64` and `linux/arm64` to Docker Hub and GHCR
after verification passes on both architectures. Docker selects the matching
architecture when pulling. Pipeline step images must also support the Docker
host's architecture; this agent does not add cross-architecture emulation.

Before the first release:

1. Create a **public** Docker Hub repository named `woodpecker-docker-extended`
   under your personal account (expected username: `ericluwj`).
2. In GitHub repository Settings > Secrets and variables > Actions, set variable
   `DOCKERHUB_USERNAME` to that account name and secret `DOCKERHUB_TOKEN` to a
   Docker Hub personal access token with **Read & Write** permission. Enter the
   token through GitHub's secret UI, never in source code or workflow inputs.
3. Ensure GitHub Actions can write GHCR packages. The workflow uses its existing
   `GITHUB_TOKEN`; GHCR package visibility is managed separately.
4. Run **Publish agent image** manually for the reviewed branch or commit.

Each run publishes `sha-<full-commit-SHA>` to both registries. Runs on `main`
also update `latest`; other refs only publish the commit tag. For the expected
Docker Hub username, pull with:

```sh
docker pull ericluwj/woodpecker-docker-extended:latest
```

The workflow summary reports digest references for both registries. Pin a digest
for deployments; commit tags can be overwritten by a rerun. A multi-registry push
is not atomic: if publication fails, inspect both registries and rerun the same
reviewed commit after fixing the failure.

GitHub Actions performs the builds; Docker Hub's free Personal plan hosts public
images subject to [pull and fair-use limits](https://docs.docker.com/docker-hub/usage/).
No Docker Hub automated-build subscription is required. The existing NUC
source-build deployment remains an available option.

## Build the agent image locally with Compose

The NUC deployment builds this agent from pinned public source. It needs no
prebuilt GHCR image or registry login for the agent. Add this build configuration
to the existing agent service, preserving its environment and state/socket mounts:

```yaml
platform: linux/amd64
build:
  context: https://github.com/ericluwj/woodpecker-docker-extended.git#91d1a0f1334ad5526807303b2187b8c840f3e7dd
  dockerfile: Dockerfile
  args:
    VERSION: "0.1.0"
    COMMIT: 91d1a0f1334ad5526807303b2187b8c840f3e7dd
```

Omit `image:` for this service: Compose assigns a local project/service image
name, and `compose pull` skips it. Explicitly build during deployment so a source
pin update cannot silently reuse an older local image. No Go installation on the
host is required; the multi-stage Dockerfile supplies the toolchain. Docker must
still reach the public source, Docker Hub base images and Go module downloads.

## Configuration

Select `WOODPECKER_BACKEND=docker-extended`. Optional
`WOODPECKER_BACKEND_DOCKER_EXTENDED_REGISTRY_MIRRORS` is a JSON object mapping
source registry hosts to cache hosts, for example:

```yaml
environment:
  WOODPECKER_BACKEND: docker-extended
  WOODPECKER_BACKEND_DOCKER_EXTENDED_REGISTRY_MIRRORS: >-
    {"docker.io":"docker-cache.example","registry.k8s.io":"k8s-cache.example","gcr.io":"gcr-cache.example","ghcr.io":"ghcr-cache.example"}
```

The matching flag is `--backend-docker-extended-registry-mirrors`. Hosts may
include ports; schemes, paths, credentials and duplicate normalized source hosts
are rejected. TLS is handled by the Docker daemon: use HTTPS caches trusted by
that daemon and do not mark them insecure. No mappings are enabled by default.

All upstream `WOODPECKER_BACKEND_DOCKER_*` connection/resource flags and
`backend_options.docker` remain available. The acquisition client uses the same
socket/host, API version and TLS settings as upstream. An invalid TLS setup
fails initialization. The Docker socket grants host-level Docker control.

## Image behavior

- Empty mapping configuration delegates unchanged to upstream Docker.
- Unmapped images and steps with registry username/password delegate unchanged.
  Configure private-origin credentials through Woodpecker's existing registry
  settings; no credentials or Docker auth files are read for mapped pulls.
- Normalize Docker Hub short names (`alpine` becomes `docker.io/library/alpine`)
  while preserving repository, tag and digest. Workflow step objects are unchanged.
- Use a cached mirror image unless the step requests forced pulling. Otherwise
  pull the mirror, consuming the complete response stream to detect failures.
- If mirror acquisition fails, use an available cached origin or attempt one
  origin pull. Forced pulls do not use either cache. Cancellation stops fallback.
- Container creation receives the selected physical image reference, so Docker
  inspection shows that reference. Logs/wait/cleanup still use upstream step IDs.
  No origin retagging is performed. Docker containers never start twice for
  fallback; create/start/job failures do not trigger an origin attempt.

Mirror fallback is anonymous. A private image needs Woodpecker registry
credentials and uses its origin directly. A service-account-backed cache may
serve additional images according to its existing access policy; only configure
trusted caches. Nested Docker/BuildKit builds do not inherit these mappings.

## Build and verify

Requires Go 1.27.0 or later (the version required by Woodpecker v3.19.0).

```sh
make test vet build
bin/woodpecker-agent-docker-extended --help
```

Local Docker integration is opt-in and uses isolated test-owned resources. If
Docker uses a non-default context, export its socket as `DOCKER_HOST` first.

```sh
export DOCKER_EXTENDED_TEST_REGISTRY_MIRRORS='{"docker.io":"mirror.gcr.io"}'
DOCKER_EXTENDED_INTEGRATION=1 go test -race -tags=integration \
  -run TestIntegrationWorkflow -timeout=10m -v ./backend/dockerextended
```

The workflow test checks mapped and deliberately broken-mirror paths, clone,
service/commands, shared workspace, actual container image selection and cleanup.
`TestIntegrationFourRegistries` additionally requires approved mappings for all
four registries and pulls a public image directly through each cache, without
fallback. These checks do not prove a NAS cache hit; correlate NAS logs separately.

See [delivery and NUC rollout](docs/rollout.md). This repository is independent
of the Apple backend; both share reference semantics, not runtime implementation.
