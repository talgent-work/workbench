//go:build integration

package infrastructure_test

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	marketplacepublicv1 "github.com/talgent-work/contracts/gen/go/marketplace/public/v1"
	"github.com/talgent-work/workbench/internal/business/agent/infrastructure/plugincatalog"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestAuthorizationReferencesArePersonalAndPreserveInactiveIdentity(t *testing.T) {
	db, _, _, client, server := setup(t)
	owner, other := "e6c7e756-007d-5236-a492-7f4b9be0b2e8", "cfe58e71-efc6-5336-8009-449ad568c42b"
	id := uuid.NewString()
	for _, record := range []struct{ id, owner, state string }{{id, owner, "reconnect_required"}, {uuid.NewString(), other, "active"}} {
		_, err := db.Exec(`INSERT INTO scm_provider_authorization
		 (id,owner_id,provider,status,access_token_ciphertext,refresh_token_ciphertext)
		 VALUES (?,?,?,?,?,?)`, record.id, record.owner, "github", record.state, "access-secret-canary", "refresh-secret-canary")
		require.NoError(t, err)
	}
	_, err := client.ListPersonalAuthorizations(t.Context(), &marketplacepublicv1.ListPersonalAuthorizationsRequest{})
	require.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	response, err := client.ListPersonalAuthorizations(as(t.Context(), owner), &marketplacepublicv1.ListPersonalAuthorizationsRequest{})
	require.NoError(t, err)
	require.Len(t, response.Authorizations, 1)
	require.Equal(t, id, response.Authorizations[0].Id)
	require.Equal(t, "reconnect_required", response.Authorizations[0].Status)
	require.NotContains(t, protojson.Format(response), "secret-canary")
	port, err := plugincatalog.NewClient(server.Client(), plugincatalog.Config{Address: server.URL})
	require.NoError(t, err)
	facts, err := port.ListPersonalAuthorizations(t.Context(), owner)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, owner, facts[0].OwnerID)
	require.Equal(t, id, facts[0].AuthorizationID, "reconnection restores this reference instead of adding a later authorization")
	_, err = db.Exec("UPDATE scm_provider_authorization SET status='revoked' WHERE id=?", id)
	require.NoError(t, err)
	facts, err = port.ListPersonalAuthorizations(t.Context(), owner)
	require.NoError(t, err)
	require.Len(t, facts, 1)
	require.Equal(t, id, facts[0].AuthorizationID)
	_, err = db.Exec("UPDATE scm_provider_authorization SET deleted_at=1 WHERE id=?", id)
	require.NoError(t, err)
	facts, err = port.ListPersonalAuthorizations(t.Context(), owner)
	require.NoError(t, err)
	require.Empty(t, facts)
}
