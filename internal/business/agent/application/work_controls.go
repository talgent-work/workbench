package application

import (
	"context"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

type ControlWork struct {
	ActorUserID, WorkID string
	ExpectedVersion     *int
}

func (a *Application) StopWork(ctx context.Context, request ControlWork) (query.WorkView, error) {
	subject := request.ActorUserID
	if err := a.WorkPrincipals.AuthorizeWork(ctx, subject, request.WorkID); err != nil {
		return query.WorkView{}, err
	}
	current, err := a.WorkViews.GetWorkView(ctx, request.WorkID)
	if err != nil {
		return query.WorkView{}, err
	}
	requestID := current.StopRequestID
	if requestID == "" {
		requestID = uuid.Must(uuid.NewV7()).String()
	}
	// Older clients still get compare-and-swap protection across this read and
	// command; current clients supply the version of their displayed Work.
	expected := request.ExpectedVersion
	if expected == nil {
		expected = &current.Version
	}
	err = a.Commands.StopWork.Handle(ctx, command.WorkControl{SubjectID: subject, WorkID: request.WorkID, RequestID: requestID, ExpectedVersion: expected})
	if err != nil {
		return query.WorkView{}, err
	}
	return a.WorkViews.GetWorkView(ctx, request.WorkID)
}

func (a *Application) ResumeWork(ctx context.Context, request ControlWork) (query.WorkView, error) {
	subject := request.ActorUserID
	if err := a.WorkPrincipals.AuthorizeWork(ctx, subject, request.WorkID); err != nil {
		return query.WorkView{}, err
	}
	err := a.Commands.ResumeWork.Handle(ctx, command.StartWork{SubjectID: subject, WorkID: request.WorkID, RequestID: uuid.Must(uuid.NewV7()).String(), ExpectedVersion: request.ExpectedVersion})
	if err != nil {
		return query.WorkView{}, err
	}
	return a.WorkViews.GetWorkView(ctx, request.WorkID)
}

func (a *Application) RestartWork(ctx context.Context, request ControlWork) (query.WorkView, error) {
	subject := request.ActorUserID
	if err := a.WorkPrincipals.AuthorizeWork(ctx, subject, request.WorkID); err != nil {
		return query.WorkView{}, err
	}
	_, err := a.Commands.RestartWork.Handle(ctx, command.StartWork{SubjectID: subject, WorkID: request.WorkID, RequestID: uuid.Must(uuid.NewV7()).String(), ExpectedVersion: request.ExpectedVersion})
	if err != nil {
		return query.WorkView{}, err
	}
	return a.WorkViews.GetWorkView(ctx, request.WorkID)
}

// CloseWork closes a Work for its owner in the Portal, where it is called
// archiving.
func (a *Application) CloseWork(ctx context.Context, request ControlWork) (query.WorkView, error) {
	subject := request.ActorUserID
	if err := a.WorkPrincipals.AuthorizeWork(ctx, subject, request.WorkID); err != nil {
		return query.WorkView{}, err
	}
	err := a.Commands.CloseWork.Handle(ctx, command.WorkControl{SubjectID: subject, WorkID: request.WorkID, RequestID: uuid.Must(uuid.NewV7()).String(), ExpectedVersion: request.ExpectedVersion})
	if err != nil {
		return query.WorkView{}, err
	}
	return a.WorkViews.GetWorkView(ctx, request.WorkID)
}
