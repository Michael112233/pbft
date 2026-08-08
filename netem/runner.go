package netem

import (
	"context"
	"os/exec"
)

type CommandRunner interface {
	Run(ctx context.Context, executable string, args ...string) ([]byte, error)
}

type ExecCommandRunner struct{}

func (ExecCommandRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, args...).CombinedOutput()
}
