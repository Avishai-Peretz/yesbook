// Package workflows starts a Cloud Workflows execution per document. The
// workflow owns retries, deadlines and the final block on failure.
package workflows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	executions "cloud.google.com/go/workflows/executions/apiv1"
	"cloud.google.com/go/workflows/executions/apiv1/executionspb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	labelJobID    = "job_id"
	labelTenantID = "tenant_id"
)

// executionsAPI is the subset of the Cloud Workflows executions client used
// here. Tests replace it with a fake.
type executionsAPI interface {
	CreateExecution(ctx context.Context, req *executionspb.CreateExecutionRequest) (*executionspb.Execution, error)
	ListExecutions(ctx context.Context, req *executionspb.ListExecutionsRequest) executionIterator
}

type executionIterator interface {
	Next() (*executionspb.Execution, error)
}

type clientAPI struct {
	client *executions.Client
}

func (c clientAPI) CreateExecution(ctx context.Context, req *executionspb.CreateExecutionRequest) (*executionspb.Execution, error) {
	return c.client.CreateExecution(ctx, req)
}

func (c clientAPI) ListExecutions(ctx context.Context, req *executionspb.ListExecutionsRequest) executionIterator {
	return c.client.ListExecutions(ctx, req)
}

type Starter struct {
	api      executionsAPI
	workflow string
}

// New targets workflow, a resource name such as
// projects/P/locations/L/workflows/yesbook-processing.
func New(client *executions.Client, workflow string) *Starter {
	return &Starter{api: clientAPI{client: client}, workflow: workflow}
}

func newStarter(api executionsAPI, workflow string) *Starter {
	return &Starter{api: api, workflow: workflow}
}

func (s *Starter) Start(ctx context.Context, tenantID, jobID string) error {
	if tenantID == "" || jobID == "" {
		return fmt.Errorf("tenant_id and job_id are required")
	}
	active, err := s.hasActiveExecution(ctx, jobID)
	if err != nil {
		return err
	}
	if active {
		return nil
	}

	arg, err := json.Marshal(map[string]string{"tenant_id": tenantID, "job_id": jobID})
	if err != nil {
		return fmt.Errorf("encode workflow argument: %w", err)
	}
	executionID := executionIDFor(jobID)
	_, err = s.api.CreateExecution(ctx, &executionspb.CreateExecutionRequest{
		Parent: s.workflow,
		Execution: &executionspb.Execution{
			// Prefer a stable name when the API accepts it so concurrent
			// Starts collide on AlreadyExists instead of creating duplicates.
			Name:     s.workflow + "/executions/" + executionID,
			Argument: string(arg),
			Labels: map[string]string{
				labelJobID:    sanitizeLabel(jobID),
				labelTenantID: sanitizeLabel(tenantID),
			},
		},
	})
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return nil
		}
		return fmt.Errorf("create workflow execution: %w", err)
	}
	return nil
}

func (s *Starter) hasActiveExecution(ctx context.Context, jobID string) (bool, error) {
	it := s.api.ListExecutions(ctx, &executionspb.ListExecutionsRequest{
		Parent:   s.workflow,
		PageSize: 20,
		View:     executionspb.ExecutionView_BASIC,
		Filter: fmt.Sprintf(`label.%s="%s" AND (state="ACTIVE" OR state="QUEUED")`,
			labelJobID, sanitizeLabel(jobID)),
	})
	_, err := it.Next()
	if err == nil {
		return true, nil
	}
	if errors.Is(err, iterator.Done) {
		return false, nil
	}
	return false, fmt.Errorf("list workflow executions: %w", err)
}

// executionIDFor returns a Workflows-safe ID derived from the job ID.
func executionIDFor(jobID string) string {
	id := strings.ToLower(jobID)
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		out = "job"
	}
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}

func sanitizeLabel(v string) string {
	v = strings.ToLower(v)
	var b strings.Builder
	started := false
	for _, r := range v {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !started {
			if r >= 'a' && r <= 'z' {
				b.WriteRune(r)
				started = true
			}
			continue
		}
		if ok {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	out := b.String()
	if out == "" {
		return "job"
	}
	if len(out) > 63 {
		return out[:63]
	}
	return out
}
