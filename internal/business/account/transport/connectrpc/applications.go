package connectrpc

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect/v2"

	accountpublicv1 "github.com/talgent-work/contracts/gen/go/account/public/v1"
	"github.com/talgent-work/contracts/gen/go/account/public/v1/accountpublicv1connect"
	"github.com/talgent-work/workbench/internal/business/account/application"
	"github.com/talgent-work/workbench/internal/business/account/application/query"
	"github.com/talgent-work/workbench/internal/business/account/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// ApplicationUseCases are what the Application procedures delegate to.
type ApplicationUseCases interface {
	RegisterApplication(ctx context.Context, cmd application.RegisterApplication) (application.RegisteredApplication, error)
	OwnedApplications(ctx context.Context, owner string) ([]query.OwnedApplication, error)
	OwnedApplication(ctx context.Context, owner, id string) (query.OwnedApplication, error)
	RotateClientSecret(ctx context.Context, cmd application.RotateClientSecret) (application.RotatedClientSecret, error)
	RevokeClientSecret(ctx context.Context, cmd application.RevokeClientSecret) (query.OwnedApplication, error)
	RenameApplication(ctx context.Context, cmd application.RenameApplication) (query.OwnedApplication, error)
	RemoveApplication(ctx context.Context, cmd application.RemoveApplication) error
}

// Applications lets a person register Applications and read the ones they
// own, as the person the entry verified.
type Applications struct {
	app ApplicationUseCases
}

var _ accountpublicv1connect.ApplicationServiceHandler = (*Applications)(nil)

func NewApplications(app ApplicationUseCases) *Applications {
	return &Applications{app: app}
}

func (h *Applications) RegisterApplication(ctx context.Context, req *accountpublicv1.RegisterApplicationRequest) (*accountpublicv1.RegisterApplicationResponse, error) {
	owner, err := currentPerson(ctx)
	if err != nil {
		return nil, err
	}
	registered, err := h.app.RegisterApplication(ctx, application.RegisterApplication{Owner: owner, Name: req.GetName()})
	if err != nil {
		return nil, applicationError(err)
	}
	return &accountpublicv1.RegisterApplicationResponse{
		Application:  applicationMessage(registered.Application),
		ClientSecret: registered.ClientSecret,
	}, nil
}

func (h *Applications) ListOwnedApplications(ctx context.Context, _ *accountpublicv1.ListOwnedApplicationsRequest) (*accountpublicv1.ListOwnedApplicationsResponse, error) {
	owner, err := currentPerson(ctx)
	if err != nil {
		return nil, err
	}
	owned, err := h.app.OwnedApplications(ctx, owner)
	if err != nil {
		return nil, applicationError(err)
	}
	applications := make([]*accountpublicv1.Application, 0, len(owned))
	for _, item := range owned {
		applications = append(applications, applicationMessage(item))
	}
	return &accountpublicv1.ListOwnedApplicationsResponse{Applications: applications}, nil
}

func (h *Applications) GetApplication(ctx context.Context, req *accountpublicv1.GetApplicationRequest) (*accountpublicv1.GetApplicationResponse, error) {
	owner, err := currentPerson(ctx)
	if err != nil {
		return nil, err
	}
	owned, err := h.app.OwnedApplication(ctx, owner, req.GetApplicationId())
	if err != nil {
		return nil, applicationError(err)
	}
	return &accountpublicv1.GetApplicationResponse{Application: applicationMessage(owned)}, nil
}

func (h *Applications) RotateClientSecret(ctx context.Context, req *accountpublicv1.RotateClientSecretRequest) (*accountpublicv1.RotateClientSecretResponse, error) {
	owner, err := currentPerson(ctx)
	if err != nil {
		return nil, err
	}
	rotated, err := h.app.RotateClientSecret(ctx, application.RotateClientSecret{Owner: owner, ApplicationID: req.GetApplicationId()})
	if err != nil {
		return nil, applicationError(err)
	}
	return &accountpublicv1.RotateClientSecretResponse{
		Application:  applicationMessage(rotated.Application),
		ClientSecret: rotated.ClientSecret,
	}, nil
}

func (h *Applications) RevokeClientSecret(ctx context.Context, req *accountpublicv1.RevokeClientSecretRequest) (*accountpublicv1.RevokeClientSecretResponse, error) {
	owner, err := currentPerson(ctx)
	if err != nil {
		return nil, err
	}
	revoked, err := h.app.RevokeClientSecret(ctx, application.RevokeClientSecret{
		Owner: owner, ApplicationID: req.GetApplicationId(), ClientSecretID: req.GetClientSecretId(),
	})
	if err != nil {
		return nil, applicationError(err)
	}
	return &accountpublicv1.RevokeClientSecretResponse{Application: applicationMessage(revoked)}, nil
}

func (h *Applications) RenameApplication(ctx context.Context, req *accountpublicv1.RenameApplicationRequest) (*accountpublicv1.RenameApplicationResponse, error) {
	owner, err := currentPerson(ctx)
	if err != nil {
		return nil, err
	}
	renamed, err := h.app.RenameApplication(ctx, application.RenameApplication{Owner: owner, ApplicationID: req.GetApplicationId(), Name: req.GetName()})
	if err != nil {
		return nil, applicationError(err)
	}
	return &accountpublicv1.RenameApplicationResponse{Application: applicationMessage(renamed)}, nil
}

func (h *Applications) RemoveApplication(ctx context.Context, req *accountpublicv1.RemoveApplicationRequest) (*accountpublicv1.RemoveApplicationResponse, error) {
	owner, err := currentPerson(ctx)
	if err != nil {
		return nil, err
	}
	if err := h.app.RemoveApplication(ctx, application.RemoveApplication{Owner: owner, ApplicationID: req.GetApplicationId()}); err != nil {
		return nil, applicationError(err)
	}
	return &accountpublicv1.RemoveApplicationResponse{}, nil
}

func currentPerson(ctx context.Context) (string, error) {
	id, ok := authn.CurrentUserID(ctx)
	if !ok {
		return "", connect.NewError(connect.CodeUnauthenticated, "account: a signed-in person is required")
	}
	return id, nil
}

func applicationError(err error) error {
	switch {
	case errors.Is(err, domain.ErrInvalidApplication):
		return connect.NewError(connect.CodeInvalidArgument, err.Error())
	case errors.Is(err, domain.ErrApplicationNotFound):
		return connect.NewError(connect.CodeNotFound, "application not found")
	case errors.Is(err, domain.ErrClientSecretNotFound):
		return connect.NewError(connect.CodeNotFound, "client secret not found")
	case errors.Is(err, domain.ErrConcurrentModification):
		return connect.NewError(connect.CodeAborted, "account: the application changed concurrently; retry")
	default:
		return connect.NewError(connect.CodeInternal, "account: the application request failed").WithCause(err)
	}
}

func applicationMessage(owned query.OwnedApplication) *accountpublicv1.Application {
	secrets := make([]*accountpublicv1.ClientSecret, 0, len(owned.Secrets))
	for _, secret := range owned.Secrets {
		state := accountpublicv1.ClientSecretState_CLIENT_SECRET_STATE_CURRENT
		if secret.State == query.ClientSecretOverlapping {
			state = accountpublicv1.ClientSecretState_CLIENT_SECRET_STATE_OVERLAPPING
		}
		secrets = append(secrets, &accountpublicv1.ClientSecret{
			Id: secret.ID, State: state, Hint: secret.Hint,
			IssuedAtUnixMs: unixMillis(secret.IssuedAt), LastUsedAtUnixMs: unixMillis(secret.LastUsedAt),
			OverlapEndsAtUnixMs: unixMillis(secret.OverlapEndsAt),
		})
	}
	return &accountpublicv1.Application{
		Id: owned.ID, Name: owned.Name, RegisteredAtUnixMs: unixMillis(owned.RegisteredAt), ClientSecrets: secrets,
		SecretOverlapSeconds: int64(owned.SecretOverlap.Seconds()), Removed: owned.Removed,
	}
}

func unixMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
