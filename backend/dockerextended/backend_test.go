package dockerextended

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/urfave/cli/v3"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
)

type fakeBackend struct {
	types.Backend
	starts []*types.Step
	mu     sync.Mutex
	err    error
	calls  []string
}

func (b *fakeBackend) StartStep(ctx context.Context, s *types.Step, task string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.starts = append(b.starts, s)
	return b.err
}
func (*fakeBackend) Load(context.Context) (*types.BackendInfo, error) {
	return &types.BackendInfo{Platform: "linux/amd64"}, nil
}
func (*fakeBackend) Flags() []cli.Flag { return nil }
func (b *fakeBackend) SetupWorkflow(context.Context, *types.Config, string) error {
	b.calls = append(b.calls, "setup")
	return nil
}
func (b *fakeBackend) TailStep(context.Context, *types.Step, string) (io.ReadCloser, error) {
	b.calls = append(b.calls, "logs")
	return io.NopCloser(strings.NewReader("logs")), nil
}
func (b *fakeBackend) WaitStep(context.Context, *types.Step, string) (*types.State, error) {
	b.calls = append(b.calls, "wait")
	return &types.State{ExitCode: 17}, nil
}
func (b *fakeBackend) DestroyStep(context.Context, *types.Step, string) error {
	b.calls = append(b.calls, "destroy-step")
	return nil
}
func (b *fakeBackend) DestroyWorkflow(context.Context, *types.Config, string) error {
	b.calls = append(b.calls, "destroy-workflow")
	return nil
}

func TestWrapperStepIsolation(t *testing.T) {
	for _, kind := range []types.StepType{types.StepTypeClone, types.StepTypeService, types.StepTypeCommands, types.StepTypePlugin, types.StepTypeCache} {
		t.Run(string(kind), func(t *testing.T) {
			upstream := &fakeBackend{err: errors.New("execution failed")}
			store := &fakeStore{}
			b := &Backend{Backend: upstream, mirrors: map[string]string{"docker.io": "cache.example"}, images: store}
			step := &types.Step{Image: "alpine:3.22", Pull: true, UUID: "unchanged", Type: kind, Environment: map[string]string{"A": "B"}}
			before := *step
			err := b.StartStep(context.Background(), step, "task")
			if !errors.Is(err, upstream.err) || len(upstream.starts) != 1 || !reflect.DeepEqual(*step, before) {
				t.Fatal("step mutated or workload retried")
			}
			got := upstream.starts[0]
			if got == step || got.Image != "cache.example/library/alpine:3.22" || got.Pull || got.UUID != step.UUID {
				t.Fatalf("bad prepared step: %+v", got)
			}
			if !reflect.DeepEqual(store.pulls, []string{"cache.example/library/alpine:3.22"}) {
				t.Fatal("execution error caused fallback")
			}
		})
	}
}
func TestWrapperPassThrough(t *testing.T) {
	for _, tc := range []struct {
		name, image string
		auth        types.Auth
		configured  bool
	}{
		{name: "unconfigured", image: "alpine"},
		{name: "unmapped", image: "quay.io/org/image:tag", configured: true},
		{name: "username", image: "alpine", auth: types.Auth{Username: "secret"}, configured: true},
		{name: "password", image: "alpine", auth: types.Auth{Password: "secret"}, configured: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &fakeBackend{}
			b := &Backend{Backend: upstream}
			if tc.configured {
				b.mirrors = map[string]string{"docker.io": "cache.example"}
			}
			step := &types.Step{Image: tc.image, Pull: true, AuthConfig: tc.auth}
			if err := b.StartStep(context.Background(), step, "task"); err != nil {
				t.Fatal(err)
			}
			if len(upstream.starts) != 1 || upstream.starts[0] != step {
				t.Fatal("pass-through changed")
			}
		})
	}
}
func TestAcquisitionFailureNeverStartsStep(t *testing.T) {
	upstream := &fakeBackend{}
	failure := errors.New("down")
	b := &Backend{Backend: upstream, mirrors: map[string]string{"docker.io": "cache.example"}, images: &fakeStore{pullErrors: map[string]error{"cache.example/library/alpine": failure, "docker.io/library/alpine": failure}}}
	if err := b.StartStep(context.Background(), &types.Step{Image: "alpine"}, "task"); err == nil || len(upstream.starts) > 0 {
		t.Fatal("started without image")
	}
}
func TestConcurrentSteps(t *testing.T) {
	upstream := &fakeBackend{}
	b := &Backend{Backend: upstream, mirrors: map[string]string{"docker.io": "cache.example"}, images: alwaysCached{}}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.StartStep(context.Background(), &types.Step{Image: "alpine"}, "task"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(upstream.starts) != 32 {
		t.Fatal("lost starts")
	}
}

type alwaysCached struct{}

func (alwaysCached) cached(context.Context, string) (bool, error) { return true, nil }
func (alwaysCached) pull(context.Context, string) error           { panic("unexpected pull") }
func TestLifecycleDelegation(t *testing.T) {
	upstream := &fakeBackend{}
	b := &Backend{Backend: upstream}
	ctx := context.Background()
	conf := &types.Config{}
	step := &types.Step{}
	if err := b.SetupWorkflow(ctx, conf, "task"); err != nil {
		t.Fatal(err)
	}
	logs, err := b.TailStep(ctx, step, "task")
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	state, err := b.WaitStep(ctx, step, "task")
	if err != nil || state.ExitCode != 17 {
		t.Fatal("wait changed")
	}
	if err := b.DestroyStep(ctx, step, "task"); err != nil {
		t.Fatal(err)
	}
	if err := b.DestroyWorkflow(ctx, conf, "task"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(upstream.calls, []string{"setup", "logs", "wait", "destroy-step", "destroy-workflow"}) {
		t.Fatal(upstream.calls)
	}
}
func TestConfigurationLoad(t *testing.T) {
	for _, flag := range []bool{false, true} {
		t.Run(map[bool]string{false: "environment", true: "flag"}[flag], func(t *testing.T) {
			b := &Backend{Backend: &fakeBackend{}}
			args := []string{"test"}
			if flag {
				args = append(args, "--"+mirrorsFlag, testMirrorsJSON)
			} else {
				t.Setenv(mirrorsEnv, testMirrorsJSON)
			}
			app := &cli.Command{Flags: b.Flags(), Action: func(ctx context.Context, c *cli.Command) error {
				_, err := b.Load(context.WithValue(ctx, types.CliCommand, c))
				return err
			}}
			if err := app.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			if len(b.mirrors) != 4 || b.Name() != Name {
				t.Fatal("config not loaded")
			}
		})
	}
	b := New()
	if _, err := b.Load(context.Background()); !errors.Is(err, types.ErrNoCliContextFound) {
		t.Fatal(err)
	}
	app := &cli.Command{Flags: b.Flags(), Action: func(ctx context.Context, c *cli.Command) error {
		_, err := b.Load(context.WithValue(ctx, types.CliCommand, c))
		return err
	}}
	if err := app.Run(context.Background(), []string{"test", "--" + mirrorsFlag, "invalid"}); err == nil {
		t.Fatal("invalid config accepted")
	}
}

func TestDockerFlagsIndependentAcrossCommands(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://docker.example:2376")
	t.Setenv("WOODPECKER_BACKEND_DOCKER_API_VERSION", "1.56")
	for range 2 {
		b := New()
		app := &cli.Command{Flags: b.Flags(), Action: func(ctx context.Context, c *cli.Command) error {
			if c.String("backend-docker-host") != "tcp://docker.example:2376" || c.String("backend-docker-api-version") != "1.56" {
				t.Fatal("Docker connection flags lost environment settings")
			}
			return nil
		}}
		if err := app.Run(context.Background(), []string{"test"}); err != nil {
			t.Fatal(err)
		}
	}
}
