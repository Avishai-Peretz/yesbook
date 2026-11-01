// Package workflows starts a Cloud Workflows execution per document. The
// workflow owns retries, deadlines and the final block on failure.
package workflows

import (
	"context"
	"encoding/json"
	"fmt"

	executions "cloud.google.com/go/workflows/executions/apiv1"
	"cloud.google.com/go/workflows/executions/apiv1/executionspb"
)

type Starter struct {
	client   *executions.Client
	workflow string
}

// New targets workflow, a resource name such as
// projects/P/locations/L/workflows/yesbook-processing.
func New(client *executions.Client, workflow string) *Starter {
	return &Starter{client: client, workflow: workflow}
}

func (s *Starter) Start(ctx context.Context, tenantID, jobID string) error {
	arg, err := json.Marshal(map[string]string{"tenant_id": tenantID, "job_id": jobID})
	if err != nil {
		return err
	}
	_, err = s.client.CreateExecution(ctx, &executionspb.CreateExecutionRequest{
		Parent:    s.workflow,
		Execution: &executionspb.Execution{Argument: string(arg)},
	})
	if err != nil {
		return fmt.Errorf("create workflow execution: %w", err)
	}
	return nil
}
