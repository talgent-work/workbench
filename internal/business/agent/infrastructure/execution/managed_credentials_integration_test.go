//go:build integration

package execution

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/application/command/commandtest"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/platformcatalog"
)

// A Managed Work on a Model Configuration gets its key and its Gateway
// credentials only through its machine's local proxy: they are delivered when
// its environment runs, a rotated key is delivered again, and an admitted stop
// withdraws them. Neither the key nor the Work credential is in the Work's
// environment, its recorded specification or its launch command.
func TestManagedWorkCredentialsReachOnlyTheLocalProxy(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	input.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
	record := f.launch(t, f.begin(t, input).ID)
	require.True(t, record.CredentialsDelivered)
	require.Equal(t, "harness-handle", record.ProcessID)
	spec, err := decodeManagedSpecification(record)
	require.NoError(t, err)
	require.Equal(t, "https://model.example/anthropic", spec.ModelEndpoint)

	f.relay.mu.Lock()
	delivered, environment, modelProxy := string(f.relay.credentials), f.relay.environment, f.relay.modelProxy
	f.relay.mu.Unlock()
	require.True(t, modelProxy, "a Model Configuration's model is always reached through the local proxy")
	require.JSONEq(t, `{"model":{"base_url":"https://model.example/anthropic","key":"`+fixtureModelKey+`"},
		"gateway":{"url":"`+managedGatewayAddress+`","identity_token":"agent-token-`+input.WorkID+`","space_token":"space-token"},
		"api":{"url":"`+managedAPIAddress+`"}}`, delivered)
	require.Equal(t, modelCredential{Key: fixtureModelKey}.digest(), record.ModelCredentialDigest)
	requireTokenRenewal(t, record)
	require.Equal(t, "fixture-primary", environment["ANTHROPIC_DEFAULT_OPUS_MODEL"], "the Model Template's environment reaches the Work")
	for _, name := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		require.NotContains(t, environment, name, "the local proxy sets the model endpoint and token per run")
	}
	encodedEnvironment, err := json.Marshal(environment)
	require.NoError(t, err)
	for _, secret := range []string{fixtureModelKey, "agent-token-" + input.WorkID, "space-token"} {
		require.NotContains(t, string(encodedEnvironment), secret)
		require.NotContains(t, record.LaunchCommand, secret)
		f.relay.mu.Lock()
		for _, process := range f.relay.created {
			require.NotContains(t, process.Command, secret)
		}
		f.relay.mu.Unlock()
	}
	require.NotContains(t, string(record.SpecificationJSON), fixtureModelKey, "the key is never recorded")
	require.True(t, strings.Contains(record.LaunchCommand, "${TALGENT_PROXY_URL}/mcp"), "the Talgent Connector goes through the local proxy")

	// A rotated key reaches the running Work.
	rotated, err := platformcatalog.NewCatalog([]command.ModelTemplate{fixtureModels().ProvidedModels()[0].Template}, []command.ModelConfiguration{{ID: "default", Name: "default", Template: "fixture-template", APIKey: "rotated-key"}})
	require.NoError(t, err)
	f.provisioning.managed.models = rotated
	authority, err := f.provisioning.authority(record)
	require.NoError(t, err)
	digest, err := authority.(credentialDelivery).modelDigest(record)
	require.NoError(t, err)
	require.NotEqual(t, record.ModelCredentialDigest, digest, "the observation sees the rotation")
	f.owners.reading = f.reading(t, record)
	long, err := f.steps.handler.Reconcile(ctx, record.ID)
	require.NoError(t, err)
	require.False(t, long)
	f.relay.mu.Lock()
	require.Contains(t, string(f.relay.credentials), `"key":"rotated-key"`)
	require.Equal(t, 2, f.relay.deliveries)
	f.relay.mu.Unlock()
	record, _, err = latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.Equal(t, digest, record.ModelCredentialDigest)

	// An admitted stop withdraws the credentials while the process reports
	// its exit.
	run := current(t, f.steps, record.ID)
	_, err = run.RequestExit(domain.WorkStopRequested{WorkID: run.WorkID, StartAdmittedAt: run.StartAdmittedAt, StopRequestID: "stop-1"})
	require.NoError(t, err)
	require.NoError(t, f.steps.executions.Save(ctx, run))
	_, err = f.steps.handler.Reconcile(ctx, record.ID)
	require.NoError(t, err)
	require.True(t, current(t, f.steps, record.ID).Credentials.Withdrawn)
	require.Equal(t, []string{"stop-1"}, f.steps.harness.Stops, "the process is asked to exit under the Work's stop")
	f.relay.mu.Lock()
	defer f.relay.mu.Unlock()
	require.Nil(t, f.relay.credentials)
	require.Equal(t, 1, f.relay.forgotten)
}

// A Work on a Model Configuration the platform no longer provides at its
// first delivery is not handed to the machine without a model, which would
// leave it on the machine's own Claude login.
func TestManagedFirstDeliveryNeedsTheModelConfigurationsKey(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	input.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
	begun := f.begin(t, input)
	// The environment is created, as the reconciliation's submission does,
	// while the Model Configuration is still provided.
	run := current(t, f.steps, begun.ID)
	require.NoError(t, f.steps.environment.PrepareCreation(ctx, run, command.CreationSupply{}))
	require.True(t, commandtest.Submit(run))
	require.NoError(t, f.steps.executions.Save(ctx, run))
	run = current(t, f.steps, begun.ID)
	answer, err := f.steps.environment.SubmitCreation(ctx, run)
	require.NoError(t, err)
	_, err = run.RecordCreationAnswer(answer)
	require.NoError(t, err)
	require.NoError(t, f.steps.executions.Save(ctx, run))
	withdrawn, err := platformcatalog.NewCatalog([]command.ModelTemplate{fixtureModels().ProvidedModels()[0].Template}, []command.ModelConfiguration{{ID: "other", Name: "other", Template: "fixture-template", APIKey: "other-key"}})
	require.NoError(t, err)
	f.provisioning.managed.models = withdrawn
	long, err := f.steps.handler.Reconcile(ctx, begun.ID)
	require.ErrorIs(t, err, errModelConfigurationNotProvided)
	require.False(t, long, "nothing is prepared before the first delivery")
	record, _, err := latestAllocation(ctx, f.db, input.WorkID)
	require.NoError(t, err)
	require.False(t, record.CredentialsDelivered)
	f.relay.mu.Lock()
	defer f.relay.mu.Unlock()
	require.Zero(t, f.relay.deliveries)
	require.Equal(t, 1, f.relay.forgotten, "a failed first delivery withdraws whatever the machine held")
}

// requireTokenRenewal checks the delivery fell due a day before the Identity
// Token it delivered expires, nothing else delivered expiring sooner.
func requireTokenRenewal(t *testing.T, record executionRecord) {
	t.Helper()
	due := time.UnixMilli(record.CredentialsExpireAt)
	require.WithinDuration(t, time.Now().Add(commandtest.AgentTokenLifetime-24*time.Hour), due, time.Minute)
}

// A redelivery while the platform no longer provides the Work's Model
// Configuration still hands the machine the Work's new Identity Token, and
// asks it to keep the model key it holds rather than fall back to the
// machine's own Claude login (Work token issuance).
func TestManagedRedeliveryRenewsTheTokenAndKeepsAWithdrawnModel(t *testing.T) {
	ctx := context.Background()
	f := newManagedWork(t)
	input := managedRequest()
	input.ContextMode = domain.ContextFresh
	input.WorkModel = domain.WorkModelSelection{ConfigurationID: "default"}
	record := f.launch(t, f.begin(t, input).ID)
	withdrawn, err := platformcatalog.NewCatalog([]command.ModelTemplate{fixtureModels().ProvidedModels()[0].Template}, []command.ModelConfiguration{{ID: "other", Name: "other", Template: "fixture-template", APIKey: "other-key"}})
	require.NoError(t, err)
	f.provisioning.managed.models = withdrawn

	renewed := command.CredentialSupply{IdentityToken: command.IdentityToken{Token: "renewed-token", ExpiresAt: time.Now().Add(commandtest.AgentTokenLifetime)}, SpaceToken: "space-token"}
	delivered, err := f.provisioning.managed.deliver(ctx, record, renewed, true, func(context.Context) error { return nil })
	require.NoError(t, err)
	require.Empty(t, delivered.ModelDigest, "the kept key is no longer the configuration's")
	f.relay.mu.Lock()
	defer f.relay.mu.Unlock()
	var body map[string]any
	require.NoError(t, json.Unmarshal(f.relay.credentials, &body))
	require.Equal(t, true, body["keep_model"])
	require.NotContains(t, body, "model")
	require.Equal(t, "renewed-token", body["gateway"].(map[string]any)["identity_token"])
}
