package runner

import (
	"context"
	"os/exec"
	"runtime"
	"time"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
)

// runShell runs the job's command and returns its combined stdout and stderr.
// A non-zero exit code is a failure.
func runShell(ctx context.Context, t *job.ShellTarget) (string, error) {
	cmd := shellCommand(ctx, t.Command)
	// When ctx ends the shell is killed. If it started child processes that
	// keep the output open, stop waiting for them after 5 seconds.
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// shellCommand uses the platform's own shell, so commands work on
// Linux and macOS (sh) as well as Windows (cmd).
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}
