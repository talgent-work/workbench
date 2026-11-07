package command_test

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	agentquery "github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
	"github.com/talgent-work/workbench/internal/testsupport/connectorcatalogtest"
)

func TestSessionUsesAuthorizedReferencesAndCapturedCapabilities(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, Capabilities: domain.CapabilityConfiguration{Prompt: "Original"}})
	require.NoError(t, err)
	store := &sessionStore{}
	handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), capacityCheck{}, store.preparation(agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})))
	id, err := handler.Handle(context.Background(), command.CreateWork{RequestID: uuid.NewString(), SubjectID: "caller", AgentID: agent.ID, Subject: domain.WorkSubject{Kind: "case", ID: "case-a"}, ProjectID: "project", WorkspaceReference: "authorized-workspace"})
	require.NoError(t, err)
	require.Equal(t, id, store.session.ID)
	require.Equal(t, "caller", store.subject)
	require.Equal(t, "authorized-workspace", store.session.WorkspaceReference)
	require.Equal(t, "user-home", store.session.HomeBinding.OwnerID)
	require.NoError(t, agent.ChangeCapabilities(domain.CapabilityConfiguration{Prompt: "Changed"}))
	require.Equal(t, "Original", store.session.CapabilitySnapshot.Prompt)
}

func TestWorkModelDefaultAndSelectionApplyToEveryPurpose(t *testing.T) {
	configured := domain.WorkModelSelection{ConfigurationID: "deployment-default"}
	explicit := domain.WorkModelSelection{ConfigurationID: "selected-model"}
	for _, purpose := range []string{"work", "project_coordination", "caller-defined"} {
		for _, selected := range []domain.WorkModelSelection{{}, explicit} {
			t.Run(purpose+"/"+selected.ConfigurationID, func(t *testing.T) {
				agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"},
					Purpose: purpose, UsageScope: domain.UsageScope{Kind: "project", Reference: "project"},
					HomeBinding: domain.HomeBinding{OwnerID: "home", Namespace: "shared"},
				})
				require.NoError(t, err)
				store := &sessionStore{}
				input := command.CreateWork{RequestID: uuid.NewString(), AgentID: agent.ID, SubjectID: "caller", WorkspaceReference: "workspace", WorkModel: selected, Subject: domain.WorkSubject{Kind: "project", ID: "project"}, ProjectID: "project"}
				handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: configured, DefaultThinkingLevel: "high"}, agentquery.WorkModelOption{Selection: explicit, DefaultThinkingLevel: "low"}), commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), capacityCheck{}, store.preparation(agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: configured, DefaultThinkingLevel: "high"}, agentquery.WorkModelOption{Selection: explicit, DefaultThinkingLevel: "low"})))
				id, err := handler.Handle(t.Context(), input)
				require.NoError(t, err)
				want := selected
				if want == (domain.WorkModelSelection{}) {
					want = configured
				}
				require.Equal(t, want, store.session.WorkModel)
				wantLevel := "low"
				if selected == (domain.WorkModelSelection{}) {
					wantLevel = "high"
				}
				require.Equal(t, wantLevel, store.session.ThinkingLevel)
				input.WorkModel = domain.WorkModelSelection{ConfigurationID: "changed"}
				_, err = handler.Handle(t.Context(), input)
				require.ErrorIs(t, err, command.ErrWorkCreationConflict, "a repeated request identity carries its original content")
				input.RequestID = uuid.NewString()
				reused, err := handler.Handle(t.Context(), input)
				require.NoError(t, err)
				require.Equal(t, id, reused)
				require.Equal(t, wantLevel, store.session.ThinkingLevel)
				require.Equal(t, want, store.session.WorkModel, "reuse retains the original model for every purpose")
			})
		}
	}
}

type agentStore struct{ agent *domain.AgentDefinition }

func (s agentStore) Get(context.Context, string) (*domain.AgentDefinition, error) {
	return s.agent, nil
}
func (agentStore) Save(context.Context, *domain.AgentDefinition) error { return nil }

type sessionStore struct {
	feedbackSessionStore
	session       *domain.AgentWork
	subject       string
	registrations int
}

// preparation answers a first-start request for the Work the store holds.
func (s *sessionStore) preparation(models agentquery.ModelConfigurations) *command.PrepareWorkExecutionHandler {
	return command.NewPrepareWorkExecutionHandler(s, s, models)
}

func (s *sessionStore) Get(context.Context, string) (*domain.AgentWork, error) {
	return s.session, nil
}
func (s *sessionStore) Save(_ context.Context, session *domain.AgentWork) error {
	s.session = session
	return nil
}

func (s *sessionStore) RegisterWork(ctx context.Context, session *domain.AgentWork, registration command.WorkRegistration) error {
	if err := s.failCommit(); err != nil {
		return err
	}
	if err := command.AnswerRetained(s.record(registration.Request), registration.Request); err != nil {
		return err
	}
	s.subject = registration.SubjectID
	s.registrations++
	return s.Save(ctx, session)
}

func (s *sessionStore) LocateWork(_ context.Context, workID string) (*domain.AgentWork, error) {
	if s.session != nil && s.session.ID == workID {
		return s.session, nil
	}
	return nil, domain.ErrWorkNotFound
}

func (s *sessionStore) FindBySubject(_ context.Context, agentID string, subject domain.WorkSubject) (*domain.AgentWork, error) {
	if s.session != nil && s.session.AgentID == agentID && s.session.Subject == subject {
		return s.session, nil
	}
	return nil, nil
}
func (s *sessionStore) AuthorizeWork(_ context.Context, subject, sessionID string) error {
	if s.session == nil || s.session.ID != sessionID || s.subject != subject {
		return access.ErrAccessDenied
	}
	return nil
}

func TestWorkCreationRejectsUnavailableModelBeforeRegistration(t *testing.T) {
	for _, purpose := range []string{"work", "project_coordination", "caller-defined"} {
		t.Run(purpose, func(t *testing.T) {
			agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, Purpose: purpose, UsageScope: domain.UsageScope{Kind: "project", Reference: "project"}, HomeBinding: domain.HomeBinding{OwnerID: "home", Namespace: "shared"}})
			require.NoError(t, err)
			store := &sessionStore{}
			handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), capacityCheck{}, store.preparation(agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})))
			_, err = handler.Handle(t.Context(), command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", RequestID: uuid.NewString(), AgentID: agent.ID, SubjectID: "caller", WorkspaceReference: "workspace", WorkModel: domain.WorkModelSelection{ConfigurationID: "unavailable"}})
			require.ErrorIs(t, err, domain.ErrModelConfigurationWithdrawn)
			_, err = handler.Handle(t.Context(), command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", AgentID: agent.ID, SubjectID: "caller", WorkspaceReference: "workspace", WorkModel: domain.WorkModelSelection{ConfigurationID: "unavailable"}, InitialExecution: &command.PrepareWorkExecution{RequestID: uuid.NewString(), WorkBranch: "talgent/TAL-1", Input: command.ExecutionInput{ID: uuid.NewString(), Text: "Do the work"}, ThinkingLevel: "high"}})
			require.ErrorIs(t, err, domain.ErrModelConfigurationWithdrawn, "the withdrawal is the reason even when a thinking level is requested")
			require.Nil(t, store.session, "a Model Configuration that is not provided establishes no Work")
			down := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), computersDown{}, store.preparation(agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})))
			_, err = down.Handle(t.Context(), command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", RequestID: uuid.NewString(), AgentID: agent.ID, SubjectID: "caller", WorkspaceReference: "workspace", WorkModel: domain.WorkModelSelection{ConfigurationID: "unavailable"}})
			require.ErrorIs(t, err, domain.ErrModelConfigurationWithdrawn, "the withdrawal is the reason even while Computer is unavailable")
		})
	}
}

// computersDown is Computer while it cannot list usable Computers.
type computersDown struct{ capacityCheck }

func (computersDown) ListUsableComputers(context.Context, string) ([]command.UsableComputer, error) {
	return nil, errors.New("computer unavailable")
}

func TestInitialThinkingLevelUsesRetainedWorkModel(t *testing.T) {
	configured := domain.WorkModelSelection{ConfigurationID: "original"}
	other := domain.WorkModelSelection{ConfigurationID: "other"}
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, Purpose: "caller-defined", UsageScope: domain.UsageScope{Kind: "project", Reference: "project"}, HomeBinding: domain.HomeBinding{OwnerID: "home", Namespace: "shared"}})
	require.NoError(t, err)
	store := &sessionStore{}
	handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: configured, ThinkingLevels: []string{"low"}}, agentquery.WorkModelOption{Selection: other, ThinkingLevels: []string{"high"}}), commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), capacityCheck{}, store.preparation(agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: configured, ThinkingLevels: []string{"low"}}, agentquery.WorkModelOption{Selection: other, ThinkingLevels: []string{"high"}})))
	input := command.CreateWork{AgentID: agent.ID, SubjectID: "caller", WorkspaceReference: "workspace", Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", InitialExecution: &command.PrepareWorkExecution{RequestID: "01940000-0000-7000-8000-000000000370", WorkBranch: "talgent/" + "TAL-370", Input: command.ExecutionInput{ID: "01940000-0000-7000-8000-000000000371", Text: "Do the work"}, ThinkingLevel: "high"}}
	_, err = handler.Handle(t.Context(), input)
	require.ErrorIs(t, err, domain.ErrInvalidWork)
	require.Nil(t, store.session)
	_, err = handler.Handle(t.Context(), input)
	require.ErrorIs(t, err, command.ErrWorkCreationCanceled, "a definitive refusal settles its request identity")
	input.InitialExecution.RequestID = uuid.NewString()
	input.InitialExecution.ThinkingLevel = "low"
	id, err := handler.Handle(t.Context(), input)
	require.NoError(t, err)
	retained := input
	input.WorkModel = other
	changed := *input.InitialExecution
	changed.RequestID, changed.ThinkingLevel = uuid.NewString(), "high"
	input.InitialExecution = &changed
	_, err = handler.Handle(t.Context(), input)
	require.ErrorIs(t, err, domain.ErrInvalidWork, "reusing the Subject's Work validates against its retained Work model, never reinterpreting it")
	require.Equal(t, "low", store.session.ThinkingLevel)
	reused, err := handler.Handle(t.Context(), retained)
	require.NoError(t, err)
	require.Equal(t, id, reused)
	require.Equal(t, configured, store.session.WorkModel)
}

type emptyPluginCatalog struct{ command.StartCatalogPort }

func (emptyPluginCatalog) ListPersonalPlugins(context.Context, string) ([]domain.PersonalPlugin, error) {
	return nil, nil
}
func (emptyPluginCatalog) ResolvePlugin(context.Context, string, string) (domain.PluginRelease, error) {
	panic("unexpected catalog resolution")
}

func (emptyPluginCatalog) ReadConnectorSelection(context.Context, string) (command.ConnectorSelection, error) {
	return connectorcatalogtest.Selection(), nil
}

func TestReusedWorkKeepsItsInputSource(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "home", Namespace: "personal"}})
	require.NoError(t, err)
	store := &sessionStore{}
	configured := domain.WorkModelSelection{ConfigurationID: "deployment-default"}
	handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: configured, DefaultThinkingLevel: "high"}), commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), capacityCheck{}, store.preparation(agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: configured, DefaultThinkingLevel: "high"})))
	input := command.CreateWork{Subject: domain.WorkSubject{Kind: "case", ID: "case"}, ProjectID: "project", RequestID: uuid.NewString(), AgentID: agent.ID, SubjectID: "caller", WorkspaceReference: "workspace", InputSource: domain.WorkInputSource{Reference: "file-space/first", SpaceToken: "first-token"}}
	id, err := handler.Handle(t.Context(), input)
	require.NoError(t, err)
	first := domain.WorkInputSource{Reference: "file-space/first", SpaceToken: "first-token"}
	require.Equal(t, first, store.session.InputSource)
	for _, changed := range []domain.WorkInputSource{{Reference: "file-space/second", SpaceToken: "second-token"}, {}} {
		input.InputSource = changed
		input.RequestID = uuid.NewString()
		reused, err := handler.Handle(t.Context(), input)
		require.NoError(t, err)
		require.Equal(t, id, reused)
		require.Equal(t, first, store.session.InputSource, "reuse neither compares nor replaces the input source")
	}
}

// Exercise the application entry through the real Work to the execution port,
// so this contract does not depend on which internal step supplies attribution.
func TestWorkDerivesCommonTraceAttributionForEverySubject(t *testing.T) {
	for _, kind := range []domain.SubjectKind{"case", "project", "review"} {
		for _, extensions := range []bool{false, true} {
			name := string(kind) + "/without-extensions"
			if extensions {
				name = string(kind) + "/with-extensions"
			}
			t.Run(name, func(t *testing.T) {
				agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, Purpose: "project_coordination",
					UsageScope: domain.UsageScope{Kind: "project", Reference: "project-a"}, HomeBinding: domain.HomeBinding{OwnerID: "home", Namespace: "shared"}})
				require.NoError(t, err)
				request := command.CreateWork{RequestID: uuid.NewString(), SubjectID: "caller", AgentID: agent.ID,
					Subject: domain.WorkSubject{Kind: kind, ID: "subject-1"}, ProjectID: "project-a"}
				if extensions {
					request.TraceAttributes = map[string]string{
						"talgent.project_id": "wrong-project", "talgent.agent_id": "wrong-agent",
						"talgent.agent_work_id": "wrong-work", "talgent.runtime_purpose": "wrong-purpose",
						"talgent.subject_kind": "wrong-kind", "talgent.subject_id": "wrong-subject",
						"talgent.case_id": "wrong-case", "domain.case_id": "case-1", "talgent.user_id": "explicit-domain-user",
					}
				}
				original := maps.Clone(request.TraceAttributes)
				store := &sessionStore{}
				handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store,
					agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}}),
					commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), capacityCheck{}, store.preparation(agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}})))
				id, err := handler.Handle(t.Context(), request)
				require.NoError(t, err)
				attributes := store.session.LaunchSpecification().TraceAttributes
				require.Equal(t, id, attributes["talgent.agent_work_id"])
				require.Equal(t, agent.ID, attributes["talgent.agent_id"])
				require.Equal(t, "project-a", attributes["talgent.project_id"])
				require.Equal(t, request.TraceAttributes["talgent.subject_kind"], attributes["talgent.subject_kind"], "the Subject is not Agent's attribute")
				require.Equal(t, "project_coordination", attributes["talgent.runtime_purpose"])
				// Agent derives no caller attribute from the Subject; the caller
				// supplies its own, such as talgent.case_id.
				if extensions {
					require.Equal(t, "wrong-case", attributes["talgent.case_id"])
				} else {
					require.NotContains(t, attributes, "talgent.case_id")
				}
				if extensions {
					require.Equal(t, "case-1", attributes["domain.case_id"])
				}
				require.NotContains(t, attributes, "talgent.user_id", "a Work of an Agent representing no person has no person, whatever its caller supplies")
				require.Equal(t, original, request.TraceAttributes)

				// Continuation derives the same identity without sharing mutable maps
				// with either callers or a previous execution request.
				want := maps.Clone(attributes)
				if extensions {
					request.TraceAttributes["domain.case_id"] = "caller-change"
				}
				attributes["domain.case_id"] = "execution-change"
				work := store.session
				report(t, work, readyReport)
				require.NoError(t, work.Stop("stop-1", false))
				report(t, work, exitedReport)
				report(t, work, releasedReport)
				require.NoError(t, work.Resume(fixtureAdmission(agent.ID)))
				require.Equal(t, want, work.LaunchSpecification().TraceAttributes)
				report(t, work, func(workID string, start time.Time) domain.Event {
					return domain.ExecutionExited{WorkID: workID, StartAdmittedAt: start, Exit: domain.HarnessExit{ExitCode: 1, Reason: "startup failed"}}
				})
				report(t, work, releasedReport)
				require.Equal(t, domain.WorkFailed, work.State)
				require.NoError(t, work.Restart(fixtureAdmission(agent.ID)))
				require.Equal(t, want, work.LaunchSpecification().TraceAttributes)
			})
		}
	}
}

// A person's Agent's Works carry that person, the one their usage rolls up to,
// replacing any person the caller names.
func TestPersonsAgentAttributesItsWorksToThePerson(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"employee"}, Purpose: "work",
		UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "employee", Namespace: "personal"}})
	require.NoError(t, err)
	store := &sessionStore{}
	handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store,
		agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}}),
		commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), capacityCheck{}, store.preparation(agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "default"}})))
	_, err = handler.Handle(t.Context(), command.CreateWork{RequestID: uuid.NewString(), StartingUserID: "employee", SubjectID: "employee", AgentID: agent.ID,
		TraceAttributes: map[string]string{"talgent.user_id": "someone-else"}})
	require.NoError(t, err)
	require.Equal(t, "employee", store.session.LaunchSpecification().TraceAttributes["talgent.user_id"])
}

// The Computer its starter chose is where the Work runs for good.
func TestAChosenComputerSeatsTheWork(t *testing.T) {
	agent, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{"caller"}, AgentName: "Fixture Agent", Purpose: "work", UsageScope: domain.UsageScope{Kind: "employee", Reference: "employee"}, HomeBinding: domain.HomeBinding{OwnerID: "user-home", Namespace: "project"}, Capabilities: domain.CapabilityConfiguration{Prompt: "Original"}})
	require.NoError(t, err)
	store := &sessionStore{}
	chosen := "01950000-0000-7000-8000-00000000c0de"
	handler := command.NewCreateWorkHandler(time.Now, agentStore{agent}, store, store, agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}}), commandtest.StartCatalog(emptyPluginCatalog{}, authorizationcatalogtest.Empty{}), capacityCheck{others: []command.UsableComputer{{ID: chosen, Cloud: true}}}, store.preparation(agentquery.NewModelConfigurations(agentquery.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}})))
	_, err = handler.Handle(t.Context(), command.CreateWork{RequestID: uuid.NewString(), SubjectID: "caller", AgentID: agent.ID, Subject: domain.WorkSubject{Kind: "case", ID: "case-a"}, ProjectID: "project", WorkspaceReference: "workspace", ComputerID: chosen})
	require.NoError(t, err)
	require.Equal(t, chosen, store.session.ComputerID)
}
