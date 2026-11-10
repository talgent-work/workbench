package execution

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

// Definition ensuring, Work creation, located layouts and native SDK
// serialization are real. Only definition lookup, Storage's grants and the
// external OpenSandbox service are controlled; this verifies that each start
// mounts the Directories its Work located by business identity: its own HOME
// and Session Workspace, its definition's Harness state, its Subject's inputs
// and its Project's knowledge.
func TestLocatedLayoutsReachTheNativeSandbox(t *testing.T) {
	ctx := context.Background()
	provider, _, fixture := sandboxClient(t)
	ensure := func(scope, owner, purpose string) *domain.AgentDefinition {
		t.Helper()
		definitions := &nativeDefaultDefinition{}
		_, err := command.NewEnsureAgentDefinitionHandler(definitions, definitions).Handle(ctx, suppliedRoleDefinition(scope, owner, purpose, "talgent-runtime", ""))
		require.NoError(t, err)
		return definitions.value
	}
	type mounts map[string]string
	submit := func(definition *domain.AgentDefinition, project, subject string) mounts {
		t.Helper()
		forward := &defaultHomeSubmission{provider: provider, root: t.TempDir()}
		snapshot, err := domain.NewCapabilitySnapshot(domain.StartupCapabilityInput{Connectors: []domain.CatalogConnector{connectorcatalogtest.Required()}, RequiredPlugins: definition.Capabilities.RequiredPlugins}, []domain.PluginRelease{{Slug: "talgent-runtime", Source: "fixture", Version: "1", RetrievalRef: "fixture:runtime", NativeEntry: []byte(`{"name":"talgent-runtime","version":"1"}`)}})
		require.NoError(t, err)
		created, isNew, err := definition.CreateWork(domain.WorkCreationRequest{StartedBy: "owner", StartingUser: "owner",
			Capabilities: snapshot, Admission: domain.StartAdmission{AdmittedAt: time.Now(), AgentID: definition.ID, DefinitionAdmitted: true, Seat: domain.ComputerSeat{ComputerID: "01a19bc3-075a-7c57-b42b-7fdf73b611ed", Kind: domain.ComputerCloud, DeskID: "desk-test"}, ModelConfigurationProvided: true},
			WorkModel: domain.WorkModelSelection{ConfigurationID: "deployment-default"},
			Subject:   domain.WorkSubject{Kind: "case", ID: subject},
			ProjectID: project,
		}, domain.WorkCreationFacts{})
		require.NoError(t, err)
		require.True(t, isNew)
		require.NoError(t, forward.StartExecution(ctx, created.LaunchSpecification()))
		require.Equal(t, created.LocatedLayout, forward.admitted.LocatedLayout, "the start mounts the layout its Work located")
		spec := fixture.create
		require.Equal(t, created.ID, spec.Env["TALGENT_WORK_ID"])
		require.Equal(t, "/home/talgent", spec.Env["HOME"])
		result := mounts{}
		for _, volume := range spec.Volumes {
			require.NotNil(t, volume.PVC)
			require.Equal(t, "nas", volume.PVC.ClaimName, "each Directory is mounted where Storage granted it")
			result[volume.MountPath] = volume.SubPath
		}
		require.Equal(t, directorySubPath(workDirectory(created.ID, "home")), result["/home/talgent"])
		require.Equal(t, directorySubPath(workDirectory(created.ID, "workspace")), result["/workspace"])
		require.Equal(t, directorySubPath(domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionAgentDefinition, Value: definition.ID}}, Name: "claude-code"}), result["/home/talgent/.claude"])
		require.Equal(t, directorySubPath(domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionSubject, Value: "case:" + subject}}, Name: "inputs"}), result["/workspace/inputs"])
		require.Equal(t, directorySubPath(domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionProject, Value: project}}, Name: "knowledge"}), result["/workspace/knowledge"])
		return result
	}

	employee := ensure("employee", uuid.NewString(), "work")
	first := submit(employee, "project-a", "case-a")
	second := submit(employee, "project-b", "case-b")
	require.NotEqual(t, first["/home/talgent"], second["/home/talgent"], "each Work has its own HOME")
	require.NotEqual(t, first["/workspace"], second["/workspace"])
	require.Equal(t, first["/home/talgent/.claude"], second["/home/talgent/.claude"], "a definition's Works share its Harness state")
	other := submit(ensure("employee", uuid.NewString(), "work"), "project-a", "case-a")
	require.NotEqual(t, first["/home/talgent/.claude"], other["/home/talgent/.claude"])
	require.Equal(t, first["/workspace/inputs"], other["/workspace/inputs"], "Works of one Subject read its inputs")
	coordination := submit(ensure("project", "project-a", "project_coordination"), "project-a", "case-c")
	require.Equal(t, first["/workspace/knowledge"], coordination["/workspace/knowledge"], "Works of one Project read its knowledge")
}

func workDirectory(workID, name string) domain.Directory {
	return domain.Directory{Owner: []domain.OwnerFact{{Condition: domain.ConditionWork, Value: workID}}, Name: name}
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
	mounts, err := directoryMounts{root: s.root}.AcquireMounts(ctx, input.WorkID, input.LocatedLayout.MountRequests())
	if err != nil {
		return err
	}
	spec, err := s.provider.specification(ctx, input, mounts, owner(input), "default-home-execution-token")
	if err != nil {
		return err
	}
	_, err = s.provider.submit(ctx, owner(input), spec, time.Now().Add(time.Duration(s.provider.config.CreationWait)))
	return err
}

// directorySubPath is where directoryMounts prepares a Directory.
func directorySubPath(directory domain.Directory) string {
	return filepath.Join("directories", infrastructure.DirectoryKey(directory))
}

// directoryMounts stands in for Storage: it prepares each requested
// Directory as a directory of root and grants it in request order, opens
// every Directory with one Space Token and holds no files.
type directoryMounts struct {
	command.StoragePort
	root string
}

func (m directoryMounts) AcquireMounts(_ context.Context, _ string, requests []domain.DirectoryMount) (domain.SandboxMounts, error) {
	mounts := make(domain.SandboxMounts, 0, len(requests))
	for _, request := range requests {
		sub := directorySubPath(request.Directory.Directory)
		if err := os.MkdirAll(filepath.Join(m.root, sub), 0o700); err != nil {
			return nil, err
		}
		mounts = append(mounts, domain.MountGrant{ClaimName: "nas", SubPath: sub, ReadOnly: request.ReadOnly})
	}
	return mounts, nil
}

func (directoryMounts) OpenDirectory(context.Context, domain.LocatedDirectory) (string, error) {
	return "space-token", nil
}

func (directoryMounts) ListFiles(context.Context, domain.Directory) ([]command.SpaceFile, error) {
	return nil, nil
}
