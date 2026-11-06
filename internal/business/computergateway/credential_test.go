package computergateway

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// A credential due for rotation is rotated over its connection: the machine
// gets the successor, saves it and says so, and only then is it confirmed;
// heartbeats and relayed calls carry on meanwhile.
func TestADueCredentialIsRotatedOverItsConnection(t *testing.T) {
	f := newFixture(t)
	f.gateway.revalidate = 50 * time.Millisecond
	conn, _, err := f.dial(t, "due")
	require.NoError(t, err)
	require.Equal(t, "welcome", read(t, conn).Type)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	require.NoError(t, err)
	var issued credentialFrame
	require.NoError(t, json.Unmarshal(data, &issued))
	require.Equal(t, credentialFrame{Type: "credential", Credential: "next", ExpiresAtUnixMS: 1_900_000_000_000}, issued)
	require.Contains(t, f.computer.seen(), "rotate c1 due")
	require.NotContains(t, f.computer.seen(), "confirm c1 next", "nothing is confirmed before the machine saved it")

	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"heartbeat"}`)))
	require.Equal(t, "heartbeat_ack", read(t, conn).Type, "the connection carries on")
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"credential_saved"}`)))
	require.Eventually(t, func() bool { return slices.Contains(f.computer.seen(), "confirm c1 next") }, 5*time.Second, 10*time.Millisecond)

	// The connection now holds the successor, which is not due: no second
	// rotation follows.
	time.Sleep(200 * time.Millisecond)
	rotations := 0
	for _, report := range f.computer.seen() {
		if report == "rotate c1 due" || report == "rotate c1 next" {
			rotations++
		}
	}
	require.Equal(t, 1, rotations)
	require.NoError(t, conn.Close(websocket.StatusNormalClosure, ""))
}

// A machine that could not save the successor keeps its current credential,
// and a later check rotates again.
func TestASuccessorTheMachineCouldNotSaveIsIssuedAgain(t *testing.T) {
	f := newFixture(t)
	f.gateway.revalidate = 50 * time.Millisecond
	conn, _, err := f.dial(t, "due")
	require.NoError(t, err)
	read(t, conn)
	require.Equal(t, "credential", read(t, conn).Type)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"credential_not_saved"}`)))
	require.Equal(t, "credential", read(t, conn).Type)
	require.NotContains(t, f.computer.seen(), "confirm c1 next")
	require.NoError(t, conn.Close(websocket.StatusNormalClosure, ""))
}

// Removing the Computer ends its open connection with the removed close
// code at the next check.
func TestARemovedComputersConnectionIsEnded(t *testing.T) {
	f := newFixture(t)
	f.gateway.revalidate = 50 * time.Millisecond
	conn, _, err := f.dial(t, "good")
	require.NoError(t, err)
	read(t, conn)
	f.computer.mu.Lock()
	f.computer.removed = true
	f.computer.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err = conn.Read(ctx)
	require.Equal(t, CloseComputerRemoved, websocket.CloseStatus(err))
	require.Eventually(t, func() bool { return slices.Contains(f.computer.seen(), "unreachable c1") }, 5*time.Second, 10*time.Millisecond)
}

// A credential revoked or expired since the connection opened ends it as
// rejected.
func TestARevokedCredentialsConnectionIsEnded(t *testing.T) {
	f := newFixture(t)
	f.gateway.revalidate = 50 * time.Millisecond
	conn, _, err := f.dial(t, "good")
	require.NoError(t, err)
	read(t, conn)
	f.computer.mu.Lock()
	f.computer.revoked = true
	f.computer.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err = conn.Read(ctx)
	require.Equal(t, CloseCredentialRejected, websocket.CloseStatus(err))
}

// Computer refusing the Gateway's control token, as while it is
// misconfigured or rotating, leaves every credential unchecked: the
// connection is kept, and checks resume once Computer accepts the Gateway.
func TestACallerFaultKeepsTheConnection(t *testing.T) {
	f := newFixture(t)
	f.gateway.revalidate = 20 * time.Millisecond
	conn, _, err := f.dial(t, "good")
	require.NoError(t, err)
	read(t, conn)
	f.computer.mu.Lock()
	f.computer.callerRefused = true
	f.computer.mu.Unlock()
	time.Sleep(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"heartbeat"}`)))
	require.Equal(t, "heartbeat_ack", read(t, conn).Type, "the connection outlived checks Computer did not answer")

	f.computer.mu.Lock()
	f.computer.callerRefused, f.computer.removed = false, true
	f.computer.mu.Unlock()
	_, _, err = conn.Read(ctx)
	require.Equal(t, CloseComputerRemoved, websocket.CloseStatus(err), "a refusal Computer answers still ends it")
}

// A Talgent Computer that does not say it supports rotation is never sent a
// credential frame it would ignore.
func TestAMachineWithoutRotationSupportIsNotRotated(t *testing.T) {
	f := newFixture(t)
	f.gateway.revalidate = 20 * time.Millisecond
	conn, _, err := f.dialWith(t, http.Header{"Authorization": {"Bearer due"}, HeaderTalgentComputerVersion: {"0.1.0"}})
	require.NoError(t, err)
	read(t, conn)
	time.Sleep(200 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, websocket.MessageText, []byte(`{"type":"heartbeat"}`)))
	require.Equal(t, "heartbeat_ack", read(t, conn).Type, "no credential frame came first")
	for _, report := range f.computer.seen() {
		require.NotContains(t, report, "rotate")
	}
	require.NoError(t, conn.Close(websocket.StatusNormalClosure, ""))
}

// A successor the machine never answers for is issued again once the wait
// for its answer passed, so a lost frame never blocks rotation.
func TestAnUnansweredSuccessorIsIssuedAgain(t *testing.T) {
	f := newFixture(t)
	f.gateway.revalidate = 20 * time.Millisecond
	f.gateway.successorWait = 100 * time.Millisecond
	conn, _, err := f.dial(t, "due")
	require.NoError(t, err)
	read(t, conn)
	require.Equal(t, "credential", read(t, conn).Type)
	require.Equal(t, "credential", read(t, conn).Type, "issued again without an answer")
	require.NotContains(t, f.computer.seen(), "confirm c1 next")
	require.NoError(t, conn.Close(websocket.StatusNormalClosure, ""))
}
