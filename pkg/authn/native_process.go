package authn

import "context"

// Native process credential headers. The credential is issued to the Work's
// current live sandbox and revoked with it.
const (
	HeaderNativeWorkID     = "X-Talgent-Work-Id"
	HeaderNativeCredential = "X-Talgent-Work-Credential"
	// HeaderSpaceToken carries a Work Agent's Space Token on its Artifact
	// commands, beside its Work credential.
	HeaderSpaceToken = "X-Talgent-Space-Token"
	// HeaderGatewayToken proves a request comes from the platform Connector,
	// where a deployment makes it the only caller of the native API.
	HeaderGatewayToken = "X-Talgent-Gateway-Token"
)

// NativeProcess is a caller-authorized scope of a Work's native process, not a
// user session. Read endpoints must constrain access to ProjectID in addition
// to normal user permissions. Native credentials cannot enter user-level
// mutation endpoints.
type NativeProcess struct {
	WorkID string
	// SubjectID is the Agent Owner the Work's process acts for.
	SubjectID string
	// AgentID is the Work's AgentDefinition; it tells a project-scoped
	// definition's Agent apart from a Project's members.
	AgentID string
	// ProjectID is the Work's Project, empty for a Work without one.
	ProjectID string
	// WorkSubjectKind and WorkSubjectID name the one object the Work is
	// about, in the kinds its creating caller defines; both are empty for a
	// Work without a Subject.
	WorkSubjectKind string
	WorkSubjectID   string
	// Purpose is the purpose of the Work's AgentDefinition.
	Purpose string
	// WorkspaceReference names the Work's Session Workspace; empty for a Work
	// without one, such as a Work on a Managed Computer.
	WorkspaceReference string
}
type nativeProcessKey struct{}

func WithNativeProcess(ctx context.Context, process NativeProcess) context.Context {
	return context.WithValue(ctx, nativeProcessKey{}, process)
}
func NativeProcessFromContext(ctx context.Context) (NativeProcess, bool) {
	process, ok := ctx.Value(nativeProcessKey{}).(NativeProcess)
	return process, ok
}
