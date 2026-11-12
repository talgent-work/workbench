package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// ChangeWorkVisibility switches the requester's own Work between private and
// public within its captured scope.
type ChangeWorkVisibility struct {
	ActorID, WorkID string
	Public          bool
}

func (a *Application) ChangeWorkVisibility(ctx context.Context, input ChangeWorkVisibility) (query.WorkView, error) {
	err := a.changeWork(ctx, input.WorkID, func(work *domain.AgentWork, definition *domain.AgentDefinition) (bool, error) {
		return work.ChangeVisibility(definition, input.ActorID, input.Public, time.Now())
	})
	if err != nil {
		return query.WorkView{}, err
	}
	return a.WorkQueries.GetWorkView(ctx, input.WorkID)
}

// GrantWorkShare lets the requesting owner share a Work with a reader the
// trusted caller admits within the Work's visibility scope.
type GrantWorkShare struct {
	RequesterID, WorkID, ReaderID string
	Admission                     domain.ReaderAdmission
}

func (a *Application) GrantWorkShare(ctx context.Context, input GrantWorkShare) error {
	return a.changeWork(ctx, input.WorkID, func(work *domain.AgentWork, definition *domain.AgentDefinition) (bool, error) {
		return work.GrantWorkShare(definition, input.RequesterID, input.ReaderID, input.Admission)
	})
}

type RevokeWorkShare struct{ ActorID, WorkID, ReaderID string }

func (a *Application) RevokeWorkShare(ctx context.Context, input RevokeWorkShare) (query.WorkView, error) {
	err := a.changeWork(ctx, input.WorkID, func(work *domain.AgentWork, definition *domain.AgentDefinition) (bool, error) {
		return work.RevokeWorkShare(definition, input.ActorID, input.ReaderID, time.Now())
	})
	if err != nil {
		return query.WorkView{}, err
	}
	return a.WorkQueries.GetWorkView(ctx, input.WorkID)
}

// RevokeDepartedReader removes a reader's Work Shares on every Work within the
// scope the trusted caller reports the reader has left.
type RevokeDepartedReader struct {
	ReaderID string
	Scope    domain.ReaderAdmission
}

func (a *Application) RevokeDepartedReader(ctx context.Context, input RevokeDepartedReader) error {
	if strings.TrimSpace(input.ReaderID) == "" || input.Scope.ProjectID == "" {
		return domain.ErrInvalidVisibility
	}
	// Every page is visited and one Work's failure does not stop the others;
	// the caller retries with the same report until no error remains.
	var failures []error
	filter := query.WorkListFilter{ProjectID: input.Scope.ProjectID, ShareReader: input.ReaderID}
	for {
		works, err := a.Queries.ListWorks.Handle(ctx, query.ListWorks{Filter: filter})
		if err != nil {
			return errors.Join(append(failures, err)...)
		}
		for _, listed := range works {
			err := a.changeWork(ctx, listed.WorkID, func(work *domain.AgentWork, _ *domain.AgentDefinition) (bool, error) {
				return work.RevokeDepartedReader(input.ReaderID, input.Scope, time.Now()), nil
			})
			if err != nil {
				failures = append(failures, fmt.Errorf("revoke departed reader on Work %s: %w", listed.WorkID, err))
			}
		}
		if len(works) < query.MaxListedWorks {
			return errors.Join(failures...)
		}
		filter.AfterID = works[len(works)-1].WorkID
	}
}

// AdmitWorkReader decides whether a reader may read a Work's message stream.
// The admission is supplied only by a trusted caller and may be empty; a
// caller starting the reader's stream also asks for the Work's current state.
type AdmitWorkReader struct {
	ReaderID, WorkID string
	Admission        domain.ReaderAdmission
	IncludeWorkState bool
}

// WorkReaderAdmission is why a reader is admitted and, when asked for, the
// Work's current state the reader starts from.
type WorkReaderAdmission struct {
	Basis domain.ReadBasis
	Work  *query.WorkView
}

func (a *Application) AdmitWorkReader(ctx context.Context, input AdmitWorkReader) (WorkReaderAdmission, error) {
	_, basis, err := a.admittedReaderWork(ctx, input)
	if err != nil || !input.IncludeWorkState {
		return WorkReaderAdmission{Basis: basis}, err
	}
	view, err := a.WorkQueries.GetWorkView(ctx, input.WorkID)
	if err != nil {
		return WorkReaderAdmission{}, err
	}
	return WorkReaderAdmission{Basis: basis, Work: &view}, nil
}

// admittedReaderWork loads the Work and admits the reader to it.
func (a *Application) admittedReaderWork(ctx context.Context, input AdmitWorkReader) (*domain.AgentWork, domain.ReadBasis, error) {
	if strings.TrimSpace(input.WorkID) == "" {
		return nil, "", domain.ErrInvalidWork
	}
	work, err := a.Works.Get(ctx, input.WorkID)
	if errors.Is(err, domain.ErrWorkNotFound) {
		return nil, "", access.ErrAccessDenied
	}
	if err != nil {
		return nil, "", err
	}
	definition, err := a.Definitions.Get(ctx, work.AgentID)
	if err != nil {
		return nil, "", err
	}
	basis, err := work.AdmitStreamReader(definition, input.ReaderID, input.Admission)
	if errors.Is(err, domain.ErrWorkReaderNotAdmitted) {
		return nil, "", errors.Join(access.ErrAccessDenied, err)
	}
	return work, basis, err
}

// changeWork applies one owner or trusted-caller decision to a Work, retrying
// a concurrent modification on fresh state, then dispatches recorded events.
func (a *Application) changeWork(ctx context.Context, workID string, decide func(*domain.AgentWork, *domain.AgentDefinition) (bool, error)) error {
	if strings.TrimSpace(workID) == "" {
		return domain.ErrInvalidWork
	}
	for range 8 {
		if err := ctx.Err(); err != nil {
			return err
		}
		work, err := a.Works.Get(ctx, workID)
		if errors.Is(err, domain.ErrWorkNotFound) {
			return access.ErrAccessDenied
		}
		if err != nil {
			return err
		}
		definition, err := a.Definitions.Get(ctx, work.AgentID)
		if err != nil {
			return err
		}
		changed, err := decide(work, definition)
		if err != nil || !changed {
			return err
		}
		if err = a.Works.Save(ctx, work); errors.Is(err, domain.ErrConcurrentModification) {
			continue
		}
		if err != nil {
			return err
		}
		return nil
	}
	return domain.ErrConcurrentModification
}

// GetWorkFacts reads a Work's collaboration facts for a trusted caller, which
// decides who sees them. Retained input, such as the first message, is message
// stream content and is left out.
func (a *Application) GetWorkFacts(ctx context.Context, workID string) (query.WorkView, error) {
	if strings.TrimSpace(workID) == "" {
		return query.WorkView{}, domain.ErrInvalidWork
	}
	view, err := a.WorkQueries.GetWorkView(ctx, workID)
	if err != nil {
		return query.WorkView{}, err
	}
	view.StartupRequest, view.WorkShares, view.StopRequestID = nil, nil, ""
	return view, nil
}

// ChangeDisplayTitle replaces the owner's Standalone Work's display title.
type ChangeDisplayTitle struct{ ActorID, WorkID, Title string }

func (a *Application) ChangeDisplayTitle(ctx context.Context, input ChangeDisplayTitle) error {
	return a.changeWork(ctx, input.WorkID, func(work *domain.AgentWork, definition *domain.AgentDefinition) (bool, error) {
		return work.ChangeDisplayTitle(definition, input.ActorID, input.Title)
	})
}
