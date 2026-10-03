// Package runner executes a job once and reports the result.
package runner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
)

// maxOutput is how much output is kept per run.
const maxOutput = 4 << 10 // 4 KB

// Run executes j once and returns the result. The run is stopped when
// the job's timeout passes or when ctx is canceled (on shutdown).
func Run(ctx context.Context, j job.Job) job.Run {
	ctx, cancel := context.WithTimeout(ctx, j.Timeout())
	defer cancel()

	run := job.Run{StartedAt: time.Now().UTC()}

	var output string
	var err error
	switch j.Type {
	case job.TypeHTTP:
		output, err = runHTTP(ctx, j.HTTP)
	default:
		err = fmt.Errorf("unknown job type %q", j.Type)
	}

	run.FinishedAt = time.Now().UTC()
	run.Output = truncate(output, maxOutput)
	run.Status = job.StatusSuccess
	if err != nil {
		run.Status = job.StatusFailed
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			run.Error = fmt.Sprintf("timed out after %s", j.Timeout())
		case errors.Is(ctx.Err(), context.Canceled):
			run.Error = "canceled: scheduler is shutting down"
		default:
			run.Error = err.Error()
		}
	}
	return run
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n...(truncated)"
}
