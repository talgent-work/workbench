//go:build integration

package agent_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	agentpublicv1 "github.com/talgent-work/contracts/gen/go/agent/public/v1"
	agentpublicv2 "github.com/talgent-work/contracts/gen/go/agent/public/v2"
	"go.uber.org/fx"

	"github.com/talgent-work/workbench/internal/business/agent"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
)

// supportDeskOwner is the person Account names as Support Desk's and Billing
// Bot's Application Owner.
const supportDeskOwner = "0193a7c2-6f1e-7c2a-9b3d-5e8f1a2b3c51"

// twoModels offers the deployment default first and a second model a
// definition may default to.
func twoModels() fx.Option {
	return fx.Decorate(func(query.ModelConfigurations) query.ModelConfigurations {
		return query.NewModelConfigurations(
			query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "deployment-default"}, ThinkingLevels: []string{"low", "medium"}, DefaultThinkingLevel: "medium"},
			query.WorkModelOption{Selection: domain.WorkModelSelection{ConfigurationID: "fast"}, ThinkingLevels: []string{"low", "high"}, DefaultThinkingLevel: "low"},
		)
	})
}

// The Application Owner reaches the Application's Agent under their own
// identity, admitted through Account's answer: they establish it, read and
// replace its Soul and change its appearance. The Application reads and
// replaces its own Soul but changes no appearance; another person, and an
// owner of a removed Application, reach none of it.
func TestTheApplicationOwnerConfiguresTheApplicationsAgent(t *testing.T) {
	m := newLifecycleModule(t).start()
	owner, desk := m.as("user", supportDeskOwner), m.as("application", supportDesk)
	stranger, billing := m.as("user", uuid.NewString()), m.as("application", billingBot)
	denied := func(err error) { t.Helper(); require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), err) }

	ensured, err := owner.work.EnsureApplicationAgent(owner.ctx, &agentpublicv1.EnsureApplicationAgentRequest{ApplicationId: supportDesk})
	require.NoError(t, err)
	require.Equal(t, "Support Desk", ensured.GetAppearance().GetName(), "it is named after the Application")
	agentID := ensured.GetAgentId()
	mine, err := desk.work.GetMyWorkAgent(desk.ctx, &agentpublicv1.GetMyWorkAgentRequest{})
	require.NoError(t, err)
	require.Equal(t, agentID, mine.GetAgentId(), "the Application's own Agent is the one its owner configures")
	_, err = stranger.work.EnsureApplicationAgent(stranger.ctx, &agentpublicv1.EnsureApplicationAgentRequest{ApplicationId: supportDesk})
	denied(err)
	_, err = owner.work.EnsureApplicationAgent(owner.ctx, &agentpublicv1.EnsureApplicationAgentRequest{ApplicationId: retiredReporting})
	denied(err)

	_, err = owner.work.ChangeAgentSoul(owner.ctx, &agentpublicv1.ChangeAgentSoulRequest{AgentId: agentID, Soul: "Answer customers kindly."})
	require.NoError(t, err)
	soul, err := desk.work.GetAgentSoul(desk.ctx, &agentpublicv1.GetAgentSoulRequest{AgentId: agentID})
	require.NoError(t, err)
	require.Equal(t, "Answer customers kindly.", soul.GetSoul())
	_, err = desk.work.ChangeAgentSoul(desk.ctx, &agentpublicv1.ChangeAgentSoulRequest{AgentId: agentID, Soul: "Answer briefly."})
	require.NoError(t, err)
	soul, err = owner.work.GetAgentSoul(owner.ctx, &agentpublicv1.GetAgentSoulRequest{AgentId: agentID})
	require.NoError(t, err)
	require.Equal(t, "Answer briefly.", soul.GetSoul())
	for _, other := range []ownerClients{stranger, billing} {
		_, err = other.work.GetAgentSoul(other.ctx, &agentpublicv1.GetAgentSoulRequest{AgentId: agentID})
		denied(err)
	}

	icon := &agentpublicv1.AgentIcon{Glyph: "rocket", Color: "blue"}
	changed, err := owner.work.ChangeWorkAgentAppearance(owner.ctx, &agentpublicv1.ChangeWorkAgentAppearanceRequest{AgentId: agentID, Name: "Desk Bot", Icon: icon})
	require.NoError(t, err)
	require.Equal(t, "Desk Bot", changed.GetAppearance().GetName())
	_, err = desk.work.ChangeWorkAgentAppearance(desk.ctx, &agentpublicv1.ChangeWorkAgentAppearanceRequest{AgentId: agentID, Name: "Mine", Icon: icon})
	require.Error(t, err, "the Application changes no appearance")
	_, err = stranger.work.ChangeWorkAgentAppearance(stranger.ctx, &agentpublicv1.ChangeWorkAgentAppearanceRequest{AgentId: agentID, Name: "Mine", Icon: icon})
	denied(err)

	personal, err := owner.work.EnsureMyWorkAgent(owner.ctx, &agentpublicv1.EnsureMyWorkAgentRequest{})
	require.NoError(t, err)
	require.NotEqual(t, agentID, personal.GetAgentId(), "the owner's own Work Agent stays apart")
}

// The Application or its owner sets the Work defaults, and a start that
// names no model, thinking level or startup Agent runs with them; a start
// that names its own keeps them. A person's Agent holds none.
func TestAStartWithoutChoicesTakesTheApplicationsWorkDefaults(t *testing.T) {
	m := newLifecycleModule(t)
	m.options = append(m.options, twoModels(), fx.Decorate(func(agent.BuiltInPluginsConfig) agent.BuiltInPluginsConfig {
		return agent.BuiltInPluginsConfig{Platform: []string{"talgent", "custom"}}
	}))
	m.start()
	owner, desk, person := m.as("user", supportDeskOwner), m.as("application", supportDesk), m.as("user", uuid.NewString())

	ensured, err := owner.work.EnsureApplicationAgent(owner.ctx, &agentpublicv1.EnsureApplicationAgentRequest{ApplicationId: supportDesk})
	require.NoError(t, err)
	agentID := ensured.GetAgentId()
	_, err = owner.work.ChangeAgentWorkDefaults(owner.ctx, &agentpublicv1.ChangeAgentWorkDefaultsRequest{AgentId: agentID, Defaults: &agentpublicv1.WorkDefaults{ModelConfigurationId: "fast", ThinkingLevel: "medium"}})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err), "a thinking level the default model does not offer")
	saved, err := desk.work.ChangeAgentWorkDefaults(desk.ctx, &agentpublicv1.ChangeAgentWorkDefaultsRequest{AgentId: agentID, Defaults: &agentpublicv1.WorkDefaults{StartupAgent: "custom:author", ModelConfigurationId: "fast", ThinkingLevel: "high"}})
	require.NoError(t, err)
	require.Equal(t, "fast", saved.GetDefaults().GetModelConfigurationId())
	read, err := owner.work.GetAgentWorkDefaults(owner.ctx, &agentpublicv1.GetAgentWorkDefaultsRequest{AgentId: agentID})
	require.NoError(t, err)
	require.Equal(t, "custom:author", read.GetDefaults().GetStartupAgent())
	require.Equal(t, "fast", read.GetOptions().GetDefaultModelConfigurationId())
	require.Equal(t, "high", read.GetOptions().GetDefaultThinkingLevel())
	require.Len(t, read.GetOptions().GetModels(), 2)

	defaulted, err := desk.start(t, uuid.NewString(), "Answer ticket 42")
	require.NoError(t, err)
	work, err := m.app.Works.Get(m.ctx, defaulted)
	require.NoError(t, err)
	require.Equal(t, domain.WorkModelSelection{ConfigurationID: "fast"}, work.WorkModel)
	require.Equal(t, "high", work.ThinkingLevel)
	require.Equal(t, "custom:author", work.CapabilitySnapshot.StartupAgent)

	chosen, err := desk.works.StartWork(desk.ctx, &agentpublicv2.StartWorkRequest{RequestId: uuid.NewString(), InitialMessage: "Answer ticket 43",
		Model: &agentpublicv1.WorkModelSelection{ConfigurationId: "deployment-default"}, ThinkingLevel: "low"})
	require.NoError(t, err)
	work, err = m.app.Works.Get(m.ctx, chosen.GetWorkId())
	require.NoError(t, err)
	require.Equal(t, domain.WorkModelSelection{ConfigurationID: "deployment-default"}, work.WorkModel, "a start's own choice wins")
	require.Equal(t, "low", work.ThinkingLevel)

	_, err = person.work.GetAgentWorkDefaults(person.ctx, &agentpublicv1.GetAgentWorkDefaultsRequest{AgentId: agentID})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	own, err := person.work.EnsureMyWorkAgent(person.ctx, &agentpublicv1.EnsureMyWorkAgentRequest{})
	require.NoError(t, err)
	_, err = person.work.GetAgentWorkDefaults(person.ctx, &agentpublicv1.GetAgentWorkDefaultsRequest{AgentId: own.GetAgentId()})
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err), "a person's Agent holds no Work defaults its owner sets")
}
