// Package apivalidator is an observational reconciler that diffs public
// API outputs between primary and secondary GitLab instances. It NEVER
// writes via API — strictly read-only. It surfaces drift that pure infra
// replication wouldn't catch (e.g. operator edits on the secondary that
// bypassed the read-only guard).
package apivalidator

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lknappich/syncctl/internal/config"
	"github.com/lknappich/syncctl/internal/metrics"
	"github.com/lknappich/syncctl/internal/reconciler"
)

const name = "api_validator"

// Reconciler diffs API counts between primary and secondary.
type Reconciler struct {
	primaryURL     string
	secondaryURL   string
	primaryToken   string
	secondaryToken string
	client         *http.Client
}

// New creates an API validator from config.
func New(cfg *config.Config) *Reconciler {
	primaryURL := strings.TrimSuffix(cfg.Primary.ExternalURL, "/") + "/api/v4"
	secondaryURL := strings.TrimSuffix(cfg.Secondaries[0].ExternalURL, "/") + "/api/v4"
	return &Reconciler{
		primaryURL:     primaryURL,
		secondaryURL:   secondaryURL,
		primaryToken:   cfg.APIValidator.PrimaryToken,
		secondaryToken: cfg.APIValidator.SecondaryToken,
		client:         &http.Client{Timeout: 30 * time.Second},
	}
}

func (r *Reconciler) Name() string { return name }

// endpoints lists the API resources we diff by count.
var endpoints = []struct {
	path  string
	label string
}{
	{"projects", "projects"},
	{"users", "users"},
	{"groups", "groups"},
	{"issues", "issues"},
	{"merge_requests", "merge_requests"},
}

// Reconcile fetches counts from both sites and compares them.
func (r *Reconciler) Reconcile(ctx context.Context) reconciler.Result {
	result := reconciler.Result{Detail: "api validation complete"}
	drifts := 0

	for _, ep := range endpoints {
		pCount, err := r.fetchCount(ctx, r.primaryURL, r.primaryToken, ep.path)
		if err != nil {
			result.Remaining++
			result.Detail = fmt.Sprintf("%s; %s primary error: %v", result.Detail, ep.label, err)
			continue
		}
		sCount, err := r.fetchCount(ctx, r.secondaryURL, r.secondaryToken, ep.path)
		if err != nil {
			result.Remaining++
			result.Detail = fmt.Sprintf("%s; %s secondary error: %v", result.Detail, ep.label, err)
			continue
		}
		if pCount != sCount {
			drifts++
			result.Remaining++
			metrics.DriftTotal.WithLabelValues("api:"+ep.label, "warning").Inc()
			result.Detail = fmt.Sprintf("%s; %s drift: primary=%d secondary=%d",
				result.Detail, ep.label, pCount, sCount)
		}
	}

	result.OK = drifts == 0
	return result
}

// fetchCount uses the X-Total header from a per_page=1 list request.
func (r *Reconciler) fetchCount(ctx context.Context, baseURL, token, path string) (int, error) {
	u := fmt.Sprintf("%s/%s?per_page=1", baseURL, path)
	parsed, err := url.Parse(u)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", parsed.String(), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	resp, err := r.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	var total int
	_, err = fmt.Sscanf(resp.Header.Get("X-Total"), "%d", &total)
	if err != nil {
		return 0, fmt.Errorf("no X-Total header: %w", err)
	}
	return total, nil
}
