package execution

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// Supplied HOME propagation, Work snapshots and native SDK serialization are real. Only
// definition/session lookup, Storage's grants and the external OpenSandbox
// service are controlled; this verifies that a start names its spaces by
// business identity, not a Kubernetes filesystem or SCM grant.
func TestSuppliedHomesReachNativeSandboxWithBusinessOwnership(t *testing.T) {
	ctx := context.Background()
	provider, _, fixture := sandboxClient(t)
	ensure := func(scope, owner, purpose string) *domain.AgentDefinition {
		t.Helper()
		definitions := &nativeDefaultDefinition{}
		_, err := command.NewEnsureAgentDefinitionHandler(definitions, definitions).Handle(ctx, suppliedRoleDefinition(scope, owner, purpose, "talgent-runtime", ""))
		require.NoError(t, err)
		return definitions.value
	}
	type mounts struct{ home, workspace string }
	seenWorkspaces := map[string]bool{}
	submit := func(definition *domain.AgentDefinition, project, subject string) mounts {
		t.Helper()
		forward := &defaultHomeSubmission{provider: provider, root: t.TempDir()}
		snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: definition.Capabilities.RequiredPlugins}, []domain.PluginRelease{{Slug: "talgent-runtime", Source: "fixture", Version: "1", RetrievalRef: "fixture:runtime", NativeEntry: []byte(`{"name":"talgent-runtime","version":"1"}`)}})
		require.NoError(t, err)
		created, isNew, err := definition.CreateWork(domain.WorkCreationRequest{StartedBy: "owner",
			Capabilities: snapshot, Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", DeskID: "desk-test"}, ModelConfigurationProvided: true},
			WorkModel:          domain.WorkModelSelection{ConfigurationID: "deployment-default"},
			Subject:            domain.WorkSubject{Kind: "case", ID: subject + "/" + uuid.NewString()},
			ProjectID:          project,
			WorkspaceReference: uuid.NewString(),
		}, domain.WorkCreationFacts{})
		require.NoError(t, err)
		require.True(t, isNew)
		session := created
		require.NoError(t, forward.StartExecution(ctx, session.LaunchSpecification()))

		require.Equal(t, definition.HomeBinding, forward.admitted.HomeBinding, "the Session must deliver its definition's HOME owner to execution, including SCM resolution")
		spec := fixture.create
		require.Equal(t, session.ID, spec.Env["TALGENT_WORK_ID"])
		require.Equal(t, "/home/talgent", spec.Env["HOME"])
		var result mounts
		for _, volume := range spec.Volumes {
			require.NotNil(t, volume.PVC)
			switch volume.MountPath {
			case "/home/talgent":
				require.Equal(t, "nas", volume.PVC.ClaimName, "the HOME is mounted where Storage granted it")
				result.home = volume.SubPath
			case "/workspace":
				require.Equal(t, "nas", volume.PVC.ClaimName, "the workspace is mounted where Storage granted it")
				result.workspace = volume.SubPath
			}
		}
		require.NotEmpty(t, result.home)
		require.Equal(t, filepath.Join("workspaces", session.WorkspaceReference), result.workspace, "the submitted workspace must belong to this Session")
		require.NotEqual(t, result.home, result.workspace)
		require.False(t, seenWorkspaces[result.workspace], "separate Sessions must never share a workspace")
		seenWorkspaces[result.workspace] = true
		return result
	}

	employeeID := uuid.NewString()
	work := ensure("employee", employeeID, "work")
	require.Equal(t, employeeID, work.HomeBinding.OwnerID, "SCM resolves employee credentials by the actual employee ID, without an Agent or project prefix")
	personalA := submit(work, "project-a", "case-a")
	personalB := submit(work, "project-b", "case-b")
	require.Equal(t, personalA.home, personalB.home, "the employee carries the same personal HOME across projects")
	otherEmployee := submit(ensure("employee", uuid.NewString(), "work"), "project-a", "case-c")
	require.NotEqual(t, personalA.home, otherEmployee.home)

	seenHomes := map[string]bool{personalA.home: true, otherEmployee.home: true}
	for _, project := range []string{"project-a", "project-b"} {
		for _, purpose := range []string{"project_coordination"} {
			definition := ensure("project", project, purpose)
			first := submit(definition, project, project+"-case-a")
			require.False(t, seenHomes[first.home], "%s/%s must be isolated from other project roles, projects and personal HOMEs", project, purpose)
			seenHomes[first.home] = true
			second := submit(definition, project, project+"-case-b")
			require.Equal(t, first.home, second.home, "a project role's Works share its HOME while retaining separate Session workspaces")
		}
	}
}

// Forward the real Work's LaunchSpecification to the real native provider.
// Lifecycle persistence/recovery is exercised by the existing provisioning tests.
type defaultHomeSubmission struct {
	provider *Sandboxes
	root     string
	admitted domain.LaunchSpecification
}

func (s *defaultHomeSubmission) StartExecution(ctx context.Context, input domain.LaunchSpecification) error {
	s.admitted = input
	mounts, err := directoryMounts{root: s.root}.AcquireMounts(ctx, input.MountRequest())
	if err != nil {
		return err
	}
	spec, err := s.provider.specification(ctx, input, mounts, owner(input), "default-home-execution-token", "work-credential-fixture")
	if err != nil {
		return err
	}
	_, err = s.provider.submit(ctx, owner(input), spec)
	return err
}

// directoryMounts stands in for Storage: it prepares each space as a
// directory of root, the one workspace per reference, HOME per owner and the
// caller's read-only mounts.
type directoryMounts struct{ root string }

func (m directoryMounts) AcquireMounts(_ context.Context, request domain.SandboxMountRequest) (domain.SandboxMounts, error) {
	workspace, home := filepath.Join("workspaces", request.WorkspaceReference), filepath.Join("homes", request.HomeOwner)
	for _, directory := range []string{workspace, home} {
		if err := os.MkdirAll(filepath.Join(m.root, directory), 0o700); err != nil {
			return domain.SandboxMounts{}, err
		}
	}
	mounts := domain.SandboxMounts{Workspace: &domain.MountGrant{ClaimName: "nas", SubPath: workspace}, Home: domain.MountGrant{ClaimName: "nas", SubPath: home}}
	for _, mount := range request.ReadOnlyMounts {
		reference := filepath.Join("references", mount.Reference)
		if err := os.MkdirAll(filepath.Join(m.root, reference), 0o700); err != nil {
			return domain.SandboxMounts{}, err
		}
		if mounts.ReadOnly == nil {
			mounts.ReadOnly = map[string]domain.MountGrant{}
		}
		mounts.ReadOnly[mount.Name] = domain.MountGrant{ClaimName: "nas", SubPath: reference, ReadOnly: true}
	}
	return mounts, nil
}
