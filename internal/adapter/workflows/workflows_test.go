package workflows

import (
	"context"
	"sync"
	"testing"

	"cloud.google.com/go/workflows/executions/apiv1/executionspb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type fakeAPI struct {
	mu        sync.Mutex
	active    []*executionspb.Execution
	created   []*executionspb.Execution
	createErr error
	listErr   error
}

func (f *fakeAPI) CreateExecution(_ context.Context, req *executionspb.CreateExecutionRequest) (*executionspb.Execution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	for _, existing := range f.created {
		if existing.Name == req.Execution.Name {
			return nil, status.Error(codes.AlreadyExists, "exists")
		}
	}
	ex := &executionspb.Execution{
		Name:     req.Execution.Name,
		Argument: req.Execution.Argument,
		Labels:   req.Execution.Labels,
		State:    executionspb.Execution_ACTIVE,
	}
	f.created = append(f.created, ex)
	f.active = append(f.active, ex)
	return ex, nil
}

func (f *fakeAPI) ListExecutions(_ context.Context, _ *executionspb.ListExecutionsRequest) executionIterator {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return &sliceIter{err: f.listErr}
	}
	items := append([]*executionspb.Execution(nil), f.active...)
	return &sliceIter{items: items}
}

type sliceIter struct {
	items []*executionspb.Execution
	err   error
	i     int
}

func (s *sliceIter) Next() (*executionspb.Execution, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.i >= len(s.items) {
		return nil, iterator.Done
	}
	item := s.items[s.i]
	s.i++
	return item, nil
}

func TestStartIsIdempotentWhenActive(t *testing.T) {
	api := &fakeAPI{}
	s := newStarter(api, "projects/p/locations/l/workflows/w")
	ctx := context.Background()

	if err := s.Start(ctx, "tenant-a", "jobabcdef0123456789abcdef01234567"); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ctx, "tenant-a", "jobabcdef0123456789abcdef01234567"); err != nil {
		t.Fatal(err)
	}
	if len(api.created) != 1 {
		t.Fatalf("created = %d, want 1", len(api.created))
	}
	if got := api.created[0].Labels[labelJobID]; got == "" {
		t.Fatal("missing job_id label")
	}
}

func TestStartTreatsAlreadyExistsAsSuccess(t *testing.T) {
	api := &fakeAPI{createErr: status.Error(codes.AlreadyExists, "exists")}
	s := newStarter(api, "projects/p/locations/l/workflows/w")
	if err := s.Start(context.Background(), "tenant-a", "jobabcdef0123456789abcdef01234567"); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionIDForSanitizes(t *testing.T) {
	if got := executionIDFor("AbC_123"); got != "abc-123" {
		t.Fatalf("executionIDFor = %q", got)
	}
}
