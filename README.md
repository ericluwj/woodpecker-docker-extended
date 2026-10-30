# Woodpecker Docker Extended

A standalone Woodpecker agent with a `docker-extended` backend that adds registry
mirror support for clone, service and step images. Container execution, networks,
workspaces, logs and cleanup use Woodpecker's upstream Docker backend.

## Compatibility

The current source targets **Woodpecker v3.20.0**. Use it with a matching
Woodpecker server; mixed-version operation is not covered by the integration
tests. Release images target **Linux AMD64 and ARM64**. Pipeline images must
support the Docker host's architecture; this agent does not add cross-architecture
emulation.

A matching-server integration test covers agent registration, platform reporting,
and successful and failed workflows. CI runs this gate on native Linux AMD64 and
ARM64 before publishing.

## Use a prebuilt image

The release workflow publishes images to Docker Hub and GHCR:

- `docker.io/ericluwj/woodpecker-docker-extended`
- `ghcr.io/ericluwj/woodpecker-docker-extended`

Releases use the pinned Woodpecker version followed by an increasing revision:
`3.20.0-1`, `3.20.0-2`, and so on. Each numbered release has a matching GitHub
Release, Git tag and multi-platform image tag in both registries. The workflow
never overwrites a numbered release. For reproducible deployments, choose a
numbered tag or pin the registry digest recorded in the GitHub release notes.

`3.20.0-latest` is a moving alias to the latest successfully published revision
for Woodpecker 3.20.0. It is available as an image tag in both registries and as a
Git tag on GitHub. The moving Git tag has no separate GitHub Release, so numbered
GitHub Releases can use immutable-release protection. There is no unversioned
`latest` alias in new releases; historical `latest` and `sha-*` tags are not
updated or removed.

```sh
docker pull ericluwj/woodpecker-docker-extended:3.20.0-latest
docker run --rm ericluwj/woodpecker-docker-extended:3.20.0-latest --version
```

Check the available releases and image version before deploying: the current
source version may be newer than the published images. GHCR package visibility
is managed separately from repository visibility.

Replace the image of an existing Woodpecker agent service with the chosen image
and set `WOODPECKER_BACKEND=docker-extended`. Keep the server connection, agent
secret, agent state and Docker socket or remote Docker connection configured as
for the upstream Docker agent. Add the mirror configuration below as needed.
Docker automatically selects the matching image architecture.

The release workflow is manually dispatched from `main`. It derives the
Woodpecker version from `go.mod` and reserves the next unused revision after
verification passes. Reserved revisions are never reused, even if a release
fails partway through; a new attempt consumes a new revision. A Woodpecker
version upgrade starts its own revision sequence at `1`.

Both numbered image tags must be uploaded and the numbered GitHub Release
created before moving aliases are promoted. Cross-service publication is not
atomic: a failure during promotion may leave aliases temporarily inconsistent.
Inspect both registries and the GitHub tag before retrying. Repository and
registry owners should enable immutability protection for numbered releases and
tags while allowing `*-latest` aliases to move.

## Build an image from source

From a checkout of the source revision you want to deploy:

```sh
docker buildx build --load \
  --build-arg VERSION=dev \
  --build-arg COMMIT="$(git rev-parse HEAD)" \
  --tag woodpecker-docker-extended:local .
```

The Dockerfile provides the Go toolchain. A local Go installation is not required
for image builds. Docker needs access to base images and Go module downloads.
The build targets the builder's native platform by default; pass
`--platform linux/amd64` or `--platform linux/arm64` to select one explicitly.

To build with Compose, add the following to your existing agent service when
running Compose from the repository root:

```yaml
build:
  context: .
  dockerfile: Dockerfile
environment:
  WOODPECKER_BACKEND: docker-extended
```

Merge this with the service's existing environment and mounts. Omit `image:` to
let Compose assign a local image name, and run `docker compose build` before
starting the service. Rebuild after changing the source revision.

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

## Development and verification

Requires Go 1.27.0 or later. Build the agent and run the unit and static checks:

```sh
make test vet build
bin/woodpecker-agent-docker-extended --help
```

Docker integration tests are opt-in and use isolated test-owned resources. If
Docker uses a non-default context, export its socket as `DOCKER_HOST` first.

```sh
export DOCKER_EXTENDED_TEST_REGISTRY_MIRRORS='{"docker.io":"mirror.gcr.io"}'
DOCKER_EXTENDED_INTEGRATION=1 go test -race -tags=integration \
  -run TestIntegrationWorkflow -timeout=10m -v ./backend/dockerextended
```

This test covers mirror and broken-mirror fallback paths, clone, services, step
commands, shared workspace, selected container images and cleanup.
`TestIntegrationFourRegistries` additionally requires configured mappings for
all four registries and pulls a public image directly through each mirror without
fallback. To verify a cache hit, correlate requests with your registry cache logs.

Run the matching-server test with:

```sh
make server-integration
```

It starts the pinned Woodpecker 3.20.0 gRPC server, scheduler and a temporary
migrated SQLite database, then launches the custom agent and checks registration,
platform, success and exit-23 failure reporting. Only the forge is mocked. It
uses the local Docker daemon; for a non-default socket, set
`WOODPECKER_BACKEND_DOCKER_HOST` explicitly. It does not use an existing server,
agent identity or token.

## License

MIT. See [LICENSE](LICENSE) and [third-party notices](THIRD_PARTY_NOTICES.md).
