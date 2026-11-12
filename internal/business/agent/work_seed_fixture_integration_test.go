//go:build integration

package agent_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/execution"
	"github.com/talgent-work/workbench/internal/testsupport/authorizationcatalogtest"
)

// The fixture definition's Owner, the Project of its Works and the capability
// differences each Work is created with.
const (
	workOwner   = "employee-a"
	workProject = "project-a"
)

var (
	workPrompt       = "session prompt"
	workStartupAgent = "custom:author"
)

// seedDefinition stores the fixture AgentDefinition as its creation does,
// with base capabilities that every Work of these cases overrides.
func seedDefinition(t *testing.T, db *xorm.Engine) *domain.AgentDefinition {
	t.Helper()
	created, err := domain.NewAgentDefinition(domain.AgentDefinitionInput{Owners: []string{workOwner}, AgentName: "Fixture Agent", Purpose: domain.StandaloneWorkPurpose,
		UsageScope:   domain.UsageScope{Kind: "employee", Reference: workOwner},
		Capabilities: domain.CapabilityConfiguration{Prompt: "base prompt", RequiredPlugins: []string{"custom"}}, WorkDefaults: domain.WorkDefaults{StartupAgent: "custom:base"},
	})
	require.NoError(t, err)
	definitions := infrastructure.NewAgentDefinitionRepository(db, time.Now)
	require.NoError(t, definitions.Save(t.Context(), created))
	stored, err := definitions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	return stored
}

// workSeed is the state a case's Work starts from.
type workSeed struct {
	subject string
	state   domain.WorkState
	// context is the current start's context mode; fresh when empty.
	context domain.ContextMode
	// turns is how many root Turns of a Working Work's process ended.
	turns int64
	// closeRequest is a close kept by a Stopped Work, or the one a Closed
	// Work applied.
	closeRequest string
}

// seededWork is a seeded Work and its current start's AgentExecution.
type seededWork struct {
	*domain.AgentWork
	execution *domain.AgentExecution
}

// seedWork stores the definition's Work of the seed's Subject in the seed's
// state, with the AgentExecution of its current start, through the Agent
// repositories: the Work is inserted as its creation commits it, Starting on a
// seated Desk of the test Cloud, and its row then updated to the state's facts.
// Its execution launched launchedProcess in its own environment; it is
// running for a Working Work and released for an ended one. No Domain Event
// is published, so the module sees only the stored state.
func seedWork(t *testing.T, db *xorm.Engine, definition *domain.AgentDefinition, seed workSeed) seededWork {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Millisecond)
	builtIns, err := domain.NewBuiltInPlugins(defaultBuiltIns.Platform, nil)
	require.NoError(t, err)
	capabilities, err := command.ResolveStartupCapabilities(ctx, commandtest.StartCatalog(modulePluginCatalog{}, authorizationcatalogtest.Empty{}), workOwner, domain.StartupCapabilityInput{
		BasePrompt: definition.Capabilities.Prompt, RequiredPlugins: builtIns.ComposeRequiredPlugins(workProject, definition.Capabilities.RequiredPlugins, nil), DefaultStartupAgent: definition.WorkDefaults.StartupAgent,
		Prompt: &workPrompt, StartupAgent: &workStartupAgent,
	})
	require.NoError(t, err)
	cloud := testCloud("10", "10Gi", "64").Cloud
	admission := domain.StartAdmission{AdmittedAt: now, AgentID: definition.ID, DefinitionAdmitted: true, ModelConfigurationProvided: true, Soul: definition.Soul,
		Seat: domain.ComputerSeat{ComputerID: cloud.ID, Kind: domain.ComputerCloud, DeskID: uuid.NewString()}}
	work, created, err := definition.CreateWork(domain.WorkCreationRequest{StartedBy: workOwner, AuthorizationInput: domain.WorkAuthorizationInput{OwnerUserID: definition.RepresentedPerson()},
		WorkModel: domain.WorkModelSelection{ConfigurationID: "deployment-default"}, Subject: domain.WorkSubject{Kind: "case", ID: seed.subject}, ProjectID: workProject,
		Capabilities: capabilities, TraceAttributes: definition.WorkTraceAttributes(nil), Admission: admission}, domain.WorkCreationFacts{})
	require.NoError(t, err)
	require.True(t, created)
	if seed.context != "" {
		work.HarnessProcess.ContextMode = seed.context
	}
	works := infrastructure.NewAgentWorkRepository(db, time.Now)
	require.NoError(t, works.Save(ctx, work))
	run := seedExecution(t, work, cloud.Name, definition.Purpose)
	work, err = works.Get(ctx, work.ID)
	require.NoError(t, err)
	if seedState(work, run, seed, now) {
		require.NoError(t, works.Save(ctx, work))
	}
	require.NoError(t, execution.NewAgentExecutionRepository(db, time.Now).Save(ctx, run))
	stored, err := works.Get(ctx, work.ID)
	require.NoError(t, err)
	return seededWork{AgentWork: stored, execution: run}
}

// seedExecution begins the execution of the Work's admitted start, seated, as
// the Policies begin it, with its environment created and process launched.
func seedExecution(t *testing.T, work *domain.AgentWork, computerName, purpose string) *domain.AgentExecution {
	t.Helper()
	var admitted domain.WorkStartAdmitted
	for _, request := range work.ExecutionRequests() {
		if start, ok := request.(domain.WorkStartAdmitted); ok {
			admitted = start
		}
	}
	launch := work.LaunchSpecification()
	launch.ComputerName, launch.Purpose = computerName, purpose
	run, err := domain.BeginExecution(domain.ExecutionBeginning{Admitted: admitted, Launch: launch, Realization: domain.RealizationCloud})
	require.NoError(t, err)
	require.True(t, commandtest.Submit(run))
	_, err = run.RecordCreationAnswer(domain.CreationAnswer{EnvironmentID: run.ID})
	require.NoError(t, err)
	launched, err := run.RecordLaunch(launchedProcess)
	require.NoError(t, err)
	require.True(t, launched)
	return run
}

// seedState sets the facts of the seed's state on the Work and its
// execution; it reports whether the Work changed from Starting.
func seedState(work *domain.AgentWork, run *domain.AgentExecution, seed workSeed, now time.Time) bool {
	p := work.HarnessProcess
	switch seed.state {
	case domain.WorkStarting:
		return false
	case domain.WorkWorking:
		// Readiness was confirmed, then every ended root Turn left the Work idle.
		work.State, p.Ready, p.IdleSince = domain.WorkWorking, true, now
		p.Turn = domain.TurnObservation{Sequence: seed.turns, Completed: seed.turns > 0}
		p.PhaseSequence = seed.turns
		run.State, run.Process.Prepared, run.Process.Started = domain.ExecutionRunning, true, true
		return true
	}
	// Every other state ended the process after readiness and released its
	// compute: a stop completed, an unrequested exit failed the Work, or a
	// close applied after the stop.
	p.Ready, p.Exited, p.ComputeReclaimed = true, true, true
	run.Process.Prepared, run.Process.Started, run.Process.Exited = true, true, true
	run.State, run.Retirement, run.RetiredAt, run.ReleasedAt, run.Desk.LeftAt = domain.ExecutionReleased, domain.RetiredByExit, now, now, now
	work.State, work.CloseRequestReference = seed.state, seed.closeRequest
	switch seed.state {
	case domain.WorkStopped, domain.WorkClosed:
		work.StopRequestReference, work.StopReason = "stop", domain.StopReasonUserStopped
	case domain.WorkFailed:
		work.FailureReason = "native_process_exited_unexpectedly (exit code 1)"
		run.Process.Exit = domain.HarnessExit{ExitCode: 1}
	}
	return true
}
