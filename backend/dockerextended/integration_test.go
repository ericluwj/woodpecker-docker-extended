//go:build integration

package dockerextended

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/urfave/cli/v3"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
)

// Explicit opt-in: creates only isolated test-owned containers/network/volume.
// No Woodpecker server connection or existing workflow is involved.
func TestIntegrationWorkflow(t *testing.T) {
	if os.Getenv("DOCKER_EXTENDED_INTEGRATION") != "1" {
		t.Skip("set DOCKER_EXTENDED_INTEGRATION=1")
	}
	mirrors := os.Getenv("DOCKER_EXTENDED_TEST_REGISTRY_MIRRORS")
	if mirrors == "" {
		t.Skip("set DOCKER_EXTENDED_TEST_REGISTRY_MIRRORS to approved HTTPS caches")
	}
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "mirror", true: "origin-fallback"}[broken], func(t *testing.T) {
			raw := mirrors
			if broken {
				raw = `{"docker.io":"127.0.0.1:1"}`
			}
			b := New()
			app := &cli.Command{Flags: b.Flags(), Action: func(ctx context.Context, c *cli.Command) error {
				ctx = context.WithValue(ctx, types.CliCommand, c)
				ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
				defer cancel()
				if _, err := b.Load(ctx); err != nil {
					return err
				}
				t.Cleanup(func() { _ = b.images.(*dockerImages).client.Close() })
				id := fmt.Sprintf("dockerext-%d", time.Now().UnixNano())
				conf := &types.Config{Network: id, Volume: id}
				makeStep := func(name string, kind types.StepType, commands ...string) *types.Step {
					return &types.Step{Name: name, UUID: id + "-" + name, Image: "docker.io/library/alpine:3.22", Type: kind,
						WorkspaceBase: "/woodpecker", WorkingDir: "/woodpecker/src", Volumes: []string{id + ":/woodpecker"},
						Commands: commands, Networks: []types.Conn{{Name: id}}}
				}
				service := makeStep("service", types.StepTypeService, "sleep 120")
				service.Detached = true
				clone := makeStep("clone", types.StepTypeClone, "printf shared > artifact")
				clone.Pull = true
				check := makeStep("check", types.StepTypeCommands, "test \"$(cat artifact)\" = shared")
				conf.Stages = []*types.Stage{{Steps: []*types.Step{service, clone, check}}}
				if err := b.SetupWorkflow(ctx, conf, id); err != nil {
					return err
				}
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 30*time.Second)
					defer stop()
					if err := b.DestroyWorkflow(cleanup, conf, id); err != nil {
						t.Error(err)
					}
					cl := b.images.(*dockerImages).client.(*client.Client)
					for _, s := range conf.Stages[0].Steps {
						if _, err := cl.ContainerInspect(cleanup, "wp_"+s.UUID, client.ContainerInspectOptions{}); !errdefs.IsNotFound(err) {
							t.Errorf("container cleanup: %v", err)
						}
					}
					if _, err := cl.VolumeInspect(cleanup, id, client.VolumeInspectOptions{}); !errdefs.IsNotFound(err) {
						t.Errorf("volume cleanup: %v", err)
					}
					if _, err := cl.NetworkInspect(cleanup, id, client.NetworkInspectOptions{}); !errdefs.IsNotFound(err) {
						t.Errorf("network cleanup: %v", err)
					}
				})
				if err := b.StartStep(ctx, service, id); err != nil {
					return err
				}
				for _, s := range []*types.Step{clone, check} {
					original := s.Image
					if err := b.StartStep(ctx, s, id); err != nil {
						return err
					}
					if s.Image != original {
						return fmt.Errorf("logical reference mutated")
					}
					state, err := b.WaitStep(ctx, s, id)
					if err != nil {
						return err
					}
					if state.ExitCode != 0 {
						logs, err := b.TailStep(ctx, s, id)
						if err == nil {
							defer logs.Close()
							out, _ := io.ReadAll(logs)
							t.Log(string(out))
						}
						return fmt.Errorf("step %s exited %d", s.Name, state.ExitCode)
					}
					actual, err := b.images.(*dockerImages).client.(*client.Client).ContainerInspect(ctx, "wp_"+s.UUID, client.ContainerInspectOptions{})
					if err != nil {
						return err
					}
					mirror, origin, err := resolveImage(original, b.mirrors)
					if err != nil {
						return err
					}
					want := mirror
					if broken {
						want = origin
					}
					if actual.Container.Config.Image != want {
						return fmt.Errorf("used image %s; want %s", actual.Container.Config.Image, want)
					}
				}
				return nil
			}}
			if err := app.Run(context.Background(), []string{"test", "--" + mirrorsFlag, raw}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Independently verify every approved source mapping. ARM64 local success does
// not establish linux/amd64 NUC or server-scheduled acceptance.
func TestIntegrationFourRegistries(t *testing.T) {
	if os.Getenv("DOCKER_EXTENDED_INTEGRATION") != "1" {
		t.Skip("explicit opt-in required")
	}
	raw := os.Getenv("DOCKER_EXTENDED_TEST_REGISTRY_MIRRORS")
	if raw == "" {
		t.Skip("approved mirror mapping required")
	}
	var mappings map[string]string
	if err := json.Unmarshal([]byte(raw), &mappings); err != nil {
		t.Fatal(err)
	}
	images := []string{"docker.io/library/alpine:3.22", "registry.k8s.io/pause:3.10", "gcr.io/distroless/static-debian12:nonroot", "ghcr.io/fluxcd/flux-cli:v2.6.4"}
	app := &cli.Command{Flags: New().Flags(), Action: func(ctx context.Context, c *cli.Command) error {
		store, err := newDockerImages(c)
		if err != nil {
			return err
		}
		defer store.client.Close()
		for _, image := range images {
			t.Run(strings.Split(image, "/")[0], func(t *testing.T) {
				mirror, origin, err := resolveImage(image, mappings)
				if err != nil || mirror == origin {
					t.Fatalf("mapping missing %v", err)
				}
				pullCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				defer cancel()
				// Direct mirror pull, without fallback: success proves cache reachability.
				if err := store.pull(pullCtx, mirror); err != nil {
					t.Fatal(err)
				}
			})
		}
		return nil
	}}
	if err := app.Run(context.Background(), []string{"test"}); err != nil {
		t.Fatal(err)
	}
}
