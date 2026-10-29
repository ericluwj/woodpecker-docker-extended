//go:build integration && test

package dockerextended

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/rs/zerolog"
	"go.woodpecker-ci.org/woodpecker/v3/rpc"

	"go.woodpecker-ci.org/woodpecker/v3/e2e/setup"
	forgeTypes "go.woodpecker-ci.org/woodpecker/v3/server/forge/types"
	"go.woodpecker-ci.org/woodpecker/v3/server/model"
	"go.woodpecker-ci.org/woodpecker/v3/server/pipeline"
)

// Uses the pinned release's real gRPC server, scheduler, migrated SQLite store,
// and pipeline compiler. Only the forge is mocked; workloads run in real Docker containers.
func TestMatchingServerAndAgent(t *testing.T) {
	if os.Getenv("DOCKER_EXTENDED_INTEGRATION") != "1" {
		t.Skip("set DOCKER_EXTENDED_INTEGRATION=1")
	}
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.WarnLevel)
	defer zerolog.SetGlobalLevel(level)
	binary := filepath.Join(t.TempDir(), "woodpecker-agent-docker-extended")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/woodpecker-agent-docker-extended")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agent: %v\n%s", err, out)
	}
	for _, tc := range []struct {
		name, command string
		status        model.StatusValue
	}{
		{"success", "echo server-to-docker-workload; printf artifact > result", model.StatusSuccess},
		{"failure", "echo failing-workload; exit 23", model.StatusFailure},
	} {
		t.Run(tc.name, func(t *testing.T) {
			yaml := []byte(fmt.Sprintf("skip_clone: true\nlabels:\n  backend: docker-extended\nsteps:\n  - name: build\n    image: alpine:3.22\n    commands:\n      - %s\n", tc.command))
			env := setup.StartServer(t.Context(), t, []*forgeTypes.FileMeta{{Name: ".woodpecker.yaml", Data: yaml}})

			// Queue before launching the agent so cleanup can retain the exact
			// compiled resource names even if the agent fails mid-workflow.
			created, err := pipeline.Create(t.Context(), env.Store, env.Fixtures.Repo, env.DummyPipeline(model.EventPush))
			if err != nil {
				t.Fatal(err)
			}
			tasks := env.Queue.Info(t.Context()).Pending
			if len(tasks) != 1 {
				t.Fatalf("queued tasks: %d", len(tasks))
			}
			var workflow rpc.Workflow
			if err := json.Unmarshal(tasks[0].Data, &workflow); err != nil {
				t.Fatal(err)
			}
			if workflow.Config == nil || workflow.Config.Network == "" || workflow.Config.Volume == "" {
				t.Fatal("missing isolated resource names")
			}
			var opts []client.Opt
			if host := os.Getenv("WOODPECKER_BACKEND_DOCKER_HOST"); host != "" {
				opts = append(opts, client.WithHost(host))
			}
			docker, err := client.New(opts...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = docker.Close() })
			t.Cleanup(func() { cleanupServerWorkflow(t, docker, workflow) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			root := t.TempDir()
			logPath := filepath.Join(root, "agent.log")
			logFile, err := os.Create(logPath)
			if err != nil {
				t.Fatal(err)
			}
			defer logFile.Close()
			cmd := exec.CommandContext(ctx, binary, "--server", env.GRPCAddr, "--grpc-token", setup.TestAgentToken, "--agent-config", filepath.Join(root, "agent.conf"), "--hostname", "docker-extended-integration", "--single-workflow", "--backend-engine", "docker-extended", "--backend-docker-host", os.Getenv("WOODPECKER_BACKEND_DOCKER_HOST"))
			cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
			cmd.WaitDelay = 10 * time.Second
			cmd.Stdout, cmd.Stderr = logFile, logFile
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(15 * time.Second):
					_ = cmd.Process.Kill()
					t.Error("agent did not stop gracefully")
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("agent did not exit after kill")
					}
				}
			})
			deadline := time.Now().Add(30 * time.Second)
			var agentID int64
			for time.Now().Before(deadline) {
				agents, err := env.Store.AgentList(&model.ListOptionsWithAll{All: true})
				if err != nil {
					t.Fatal(err)
				}
				for _, agent := range agents {
					if agent.Backend == "docker-extended" {
						if !strings.HasPrefix(agent.Version, "3.20.0+docker-extended.") {
							t.Fatalf("version %s", agent.Version)
						}
						if agent.Platform != "linux/"+runtime.GOARCH {
							t.Fatalf("platform %s", agent.Platform)
						}
						agentID = agent.ID
					}
				}
				if agentID != 0 {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if agentID == 0 {
				data, _ := os.ReadFile(logPath)
				t.Fatalf("agent did not register: %s", data)
			}
			finished := setup.WaitForPipelineStatus(t, env.Store, created.ID, tc.status, 5*time.Minute)
			workflows, err := env.Store.WorkflowGetTree(finished)
			if err != nil {
				t.Fatal(err)
			}
			if len(workflows) != 1 || workflows[0].AgentID != agentID || workflows[0].State != tc.status {
				t.Fatalf("unexpected workflow result: %+v", workflows)
			}
			wantExit := 0
			if tc.status == model.StatusFailure {
				wantExit = 23
			}
			if len(workflows[0].Children) != 1 || workflows[0].Children[0].ExitCode != wantExit {
				t.Fatalf("unexpected step exit: %+v", workflows[0].Children)
			}
			select {
			case err := <-done:
				if err != nil {
					data, _ := os.ReadFile(logPath)
					t.Fatalf("agent exited: %v %s", err, data)
				}
				done <- nil
			case <-time.After(30 * time.Second):
				t.Fatal("single-workflow agent did not exit")
			}
		})
	}
}

// Called after the agent exits. Assert normal cleanup and, on failure, remove
// only names captured from this test's isolated server before agent startup.
func cleanupServerWorkflow(t *testing.T, docker *client.Client, workflow rpc.Workflow) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, stage := range workflow.Config.Stages {
		for _, step := range stage.Steps {
			name := "wp_" + step.UUID
			if _, err := docker.ContainerInspect(ctx, name, client.ContainerInspectOptions{}); !errdefs.IsNotFound(err) {
				t.Errorf("agent container cleanup %s: %v", name, err)
				if _, err := docker.ContainerRemove(ctx, name, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !errdefs.IsNotFound(err) {
					t.Error(err)
				}
			}
		}
	}
	if _, err := docker.VolumeInspect(ctx, workflow.Config.Volume, client.VolumeInspectOptions{}); !errdefs.IsNotFound(err) {
		t.Errorf("agent volume cleanup: %v", err)
		if _, err := docker.VolumeRemove(ctx, workflow.Config.Volume, client.VolumeRemoveOptions{}); err != nil && !errdefs.IsNotFound(err) {
			t.Error(err)
		}
	}
	if _, err := docker.NetworkInspect(ctx, workflow.Config.Network, client.NetworkInspectOptions{}); !errdefs.IsNotFound(err) {
		t.Errorf("agent network cleanup: %v", err)
		if _, err := docker.NetworkRemove(ctx, workflow.Config.Network, client.NetworkRemoveOptions{}); err != nil && !errdefs.IsNotFound(err) {
			t.Error(err)
		}
	}
}
