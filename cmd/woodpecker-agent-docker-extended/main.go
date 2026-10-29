package main

import (
	"context"

	"go.woodpecker-ci.org/woodpecker/v3/cmd/agent/core"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
	"go.woodpecker-ci.org/woodpecker/v3/version"

	"github.com/ericluwj/woodpecker-docker-extended/backend/dockerextended"
)

var buildVersion = "dev"
var buildCommit = "unknown"

func main() {
	version.Version = "3.20.0+docker-extended." + buildVersion + "." + buildCommit
	core.RunAgent(context.Background(), []types.Backend{dockerextended.New()})
}
