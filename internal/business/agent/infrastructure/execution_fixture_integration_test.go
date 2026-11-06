//go:build integration

package infrastructure_test

import (
	"context"

	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

type executionPort struct{}

func (executionPort) StartExecution(context.Context, domain.StartExecutionRequest) error { return nil }
func (executionPort) StopExecution(context.Context, string, string) error {
	return nil
}
func (executionPort) ReclaimCompute(context.Context, string) error { return nil }

func (executionPort) RetireFencedRun(context.Context, string) error { return nil }

type startupAuthority struct{}

func (startupAuthority) AuthorizeWork(context.Context, string, string) error { return nil }
func (startupAuthority) AdmitRoom(context.Context) error                     { return nil }

type startupExecutionPort struct {
	executionPort
	onStart func(domain.StartExecutionRequest) error
	onStop  func()
}

func (p startupExecutionPort) StartExecution(_ context.Context, request domain.StartExecutionRequest) error {
	return p.onStart(request)
}

func (p startupExecutionPort) StopExecution(context.Context, string, string) error {
	if p.onStop != nil {
		p.onStop()
	}
	return nil
}

type recoveryExecutionPort struct {
	executionPort
	err      error
	starts   []domain.StartExecutionRequest
	stops    []string
	reclaims []string
}

func (p *recoveryExecutionPort) StartExecution(_ context.Context, request domain.StartExecutionRequest) error {
	p.starts = append(p.starts, request)
	return p.err
}
func (p *recoveryExecutionPort) StopExecution(_ context.Context, _ string, requestID string) error {
	p.stops = append(p.stops, requestID)
	return p.err
}
func (p *recoveryExecutionPort) ReclaimCompute(_ context.Context, workID string) error {
	p.reclaims = append(p.reclaims, workID)
	return p.err
}

func (executionPort) CancelRunningTurn(context.Context, string) error { return nil }

func (*recoveryExecutionPort) CancelRunningTurn(context.Context, string) error { return nil }
