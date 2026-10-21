// Package dockerextended wraps Woodpecker's Docker backend with registry mirrors.
package dockerextended

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/docker"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
)

const Name = "docker-extended"
const mirrorsFlag = "backend-docker-extended-registry-mirrors"
const mirrorsEnv = "WOODPECKER_BACKEND_DOCKER_EXTENDED_REGISTRY_MIRRORS"

// Backend delegates container lifecycle to the pinned upstream implementation.
// Configuration is immutable after Load; each StartStep uses its own step copy.
type Backend struct {
	types.Backend
	mirrors map[string]string
	images  imageStore
}

var _ types.Backend = (*Backend)(nil)

func New() *Backend           { return &Backend{Backend: docker.New()} }
func (*Backend) Name() string { return Name }
func (b *Backend) Flags() []cli.Flag {
	// Upstream returns global flag pointers. Give each agent command its own
	// parsing state so separate embedded commands cannot lose env/host settings.
	flags := make([]cli.Flag, 0, len(b.Backend.Flags())+1)
	for _, flag := range b.Backend.Flags() {
		switch f := flag.(type) {
		case *cli.StringFlag:
			copy := *f
			flags = append(flags, &copy)
		case *cli.BoolFlag:
			copy := *f
			flags = append(flags, &copy)
		case *cli.Int64Flag:
			copy := *f
			flags = append(flags, &copy)
		default:
			panic("unsupported upstream Docker flag type; review dependency upgrade")
		}
	}
	return append(flags, &cli.StringFlag{Name: mirrorsFlag, Usage: "JSON source-registry to HTTPS cache host map; falls back to origin once", Sources: cli.EnvVars(mirrorsEnv)})
}
func (b *Backend) Load(ctx context.Context) (*types.BackendInfo, error) {
	c, ok := ctx.Value(types.CliCommand).(*cli.Command)
	if !ok {
		return nil, types.ErrNoCliContextFound
	}
	mirrors, err := parseRegistryMirrors(c.String(mirrorsFlag))
	if err != nil {
		return nil, err
	}
	// Validate acquisition settings before upstream opens a connection. In
	// particular an invalid TLS configuration must not silently fall back.
	var store *dockerImages
	if len(mirrors) > 0 {
		store, err = newDockerImages(c)
		if err != nil {
			return nil, err
		}
	}
	info, err := b.Backend.Load(ctx)
	if err != nil {
		if store != nil {
			_ = store.client.Close()
		}
		return nil, err
	}
	b.mirrors = mirrors
	if store != nil {
		b.images = store
	}
	return info, nil
}
func (b *Backend) StartStep(ctx context.Context, step *types.Step, taskUUID string) error {
	if len(b.mirrors) == 0 || step.AuthConfig.Username != "" || step.AuthConfig.Password != "" {
		return b.Backend.StartStep(ctx, step, taskUUID)
	}
	mirror, origin, err := resolveImage(step.Image, b.mirrors)
	if err != nil {
		return err
	}
	if mirror == origin {
		return b.Backend.StartStep(ctx, step, taskUUID)
	}
	image, err := prepareImage(ctx, b.images, mirror, origin, step.Pull)
	if err != nil {
		return fmt.Errorf("prepare step image: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	prepared := *step
	prepared.Image = image
	prepared.Pull = false
	// No credentials are ever supplied to mapped pulls. Upstream receives only
	// an already acquired image; execution errors are returned without fallback.
	return b.Backend.StartStep(ctx, &prepared, taskUUID)
}
