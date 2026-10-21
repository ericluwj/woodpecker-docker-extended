# Woodpecker Docker Extended

A standalone Woodpecker v3.19.0 agent with backend `docker-extended`. It wraps
upstream Docker container execution, networks, workspaces, logs and cleanup,
adding registry mirror acquisition for clone, service and step images.

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
