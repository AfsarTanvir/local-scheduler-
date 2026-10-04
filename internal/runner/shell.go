package runner

import (
	"bytes"
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

	// Keep only the start of the output while the command runs. Collecting
	// all of it first would let a command that prints a lot use up the
	// server's memory. One byte more than maxOutput is kept, so Run can
	// tell that the output was cut.
	out := &limitedBuffer{limit: maxOutput + 1}
	cmd.Stdout = out
	cmd.Stderr = out // same writer: exec never calls Write from two goroutines at once

	// When ctx ends the shell is killed. If it started child processes that
	// keep the output open, stop waiting for them after 5 seconds.
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	return out.buf.String(), err
}

// shellCommand uses the platform's own shell, so commands work on
// Linux and macOS (sh) as well as Windows (cmd).
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.CommandContext(ctx, "cmd", "/C", command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

// limitedBuffer keeps the first limit bytes written to it and drops the rest.
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := b.limit - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	// Report everything as written, so the command keeps running normally.
	return len(p), nil
}
