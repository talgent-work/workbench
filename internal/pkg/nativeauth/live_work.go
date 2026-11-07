// Package nativeauth resolves a Work's native process from Agent, the one
// authority over Work credentials: its Work, the Work's optional Project and
// Subject, and the definition's purpose. Each product context then admits
// the process under its own rules.
package nativeauth

import (
	"context"
	"errors"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	agentprivatev1 "github.com/talgent-work/contracts/gen/agent/private/v1"
	"github.com/talgent-work/contracts/gen/agent/private/v1/agentprivatev1connect"
	"github.com/talgent-work/workbench/internal/pkg/authn"
)

// LiveWorkClient resolves a native process whose credential belongs to its
// Work's live sandbox. It admits reads, and so does not wait for the Work to
// be interactive: a session can read before its first turn.
type LiveWorkClient struct {
	agent agentprivatev1connect.AgentServiceClient
	token string
}

var _ authn.NativeProcessVerifier = (*LiveWorkClient)(nil)

func NewLiveWorkClient(http connecthttp.HTTPClient, address, token string) (*LiveWorkClient, error) {
	if http == nil || strings.TrimSpace(address) == "" || strings.TrimSpace(token) == "" {
		return nil, errors.New("native authentication requires the Agent address and control token")
	}
	return &LiveWorkClient{agent: agentprivatev1connect.NewAgentServiceClient(connect.NewClient(connecthttp.NewTransport(http, address, connecthttp.WithReadMaxBytes(0)))), token: token}, nil
}

var errRejected = connect.NewError(connect.CodeUnauthenticated, "native process credential rejected")

func (c *LiveWorkClient) VerifyNativeProcess(ctx context.Context, workID, credential string) (authn.NativeProcess, error) {
	return c.verify(ctx, workID, credential, false)
}

// verify asks Agent for the credential's Work and, for an action, whether the
// Work acts.
func (c *LiveWorkClient) verify(ctx context.Context, workID, credential string, acting bool) (authn.NativeProcess, error) {
	workID, credential = strings.TrimSpace(workID), strings.TrimSpace(credential)
	if workID == "" || credential == "" {
		return authn.NativeProcess{}, errRejected
	}
	request := &agentprivatev1.VerifyWorkCredentialRequest{WorkId: workID, Credential: credential, RequireActing: acting}
	ctx, info := connect.NewClientContext(ctx)
	info.
		// The platform authenticates to Agent; the sandbox's own credential
		// travels only in the request body being verified.
		RequestHeader().Set("Authorization", "Bearer "+c.token)
	verified, err := c.agent.VerifyWorkCredential(ctx, request)
	if err != nil {
		switch connect.CodeOf(err) {
		case connect.CodeUnauthenticated, connect.CodePermissionDenied, connect.CodeNotFound:
			return authn.NativeProcess{}, errRejected
		case connect.CodeFailedPrecondition:
			if acting {
				return authn.NativeProcess{}, errNotActing
			}
		}
		return authn.NativeProcess{}, err
	}
	work := verified
	if work.GetWorkId() != workID || strings.TrimSpace(work.GetSubjectId()) == "" {
		return authn.NativeProcess{}, errRejected
	}
	return authn.NativeProcess{
		WorkID: workID, SubjectID: work.GetSubjectId(), AgentID: work.GetAgentId(), ProjectID: strings.TrimSpace(work.GetProjectId()),
		WorkSubjectKind: strings.TrimSpace(work.GetSubject().GetKind()), WorkSubjectID: strings.TrimSpace(work.GetSubject().GetId()),
		Purpose: strings.TrimSpace(work.GetPurpose()), WorkspaceReference: work.GetWorkspaceReference(),
	}, nil
}

// ActingWorkClient resolves a native process that acts: Agent admits its
// credential only while its Work acts, as Agent decides.
type ActingWorkClient struct {
	live *LiveWorkClient
}

var _ authn.NativeProcessVerifier = (*ActingWorkClient)(nil)

func NewActingWorkClient(live *LiveWorkClient) *ActingWorkClient {
	return &ActingWorkClient{live: live}
}

var errNotActing = connect.NewError(connect.CodeUnauthenticated, "the Work is not acting")

func (c *ActingWorkClient) VerifyNativeProcess(ctx context.Context, workID, credential string) (authn.NativeProcess, error) {
	return c.live.verify(ctx, workID, credential, true)
}
