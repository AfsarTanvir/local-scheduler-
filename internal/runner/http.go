package runner

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/AfsarTanvir/local-scheduler-/internal/job"
)

// runHTTP sends the job's request. Any 2xx response is a success.
// The timeout comes from ctx, so the default client is fine here.
func runHTTP(ctx context.Context, t *job.HTTPTarget) (string, error) {
	req, err := http.NewRequestWithContext(ctx, t.Method, t.URL, strings.NewReader(t.Body))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "local-scheduler")
	for k, v := range t.Headers {
		req.Header.Set(k, v)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxOutput))
	output := resp.Status + "\n" + string(body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return output, fmt.Errorf("unexpected status %s", resp.Status)
	}
	return output, nil
}
