package computergateway

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/coder/websocket"

	computerprivatev1 "github.com/talgent-work/contracts/gen/go/computer/private/v1"
)

// Revalidate is how often each connection's Computer Credential is checked
// again: a removed Computer, or a credential revoked or expired since the
// connection opened, ends the connection by then, and a credential due for
// rotation is rotated.
const Revalidate = HeartbeatInterval

// SuccessorWait bounds the wait for a machine's answer to a credential
// frame: a successor still unanswered by then, sent to a machine that never
// got it or failed to answer, is issued again once Computer's reservation
// of it passed (domain.CredentialOffer); both change together.
const SuccessorWait = 10 * time.Minute

// HeaderCredentialRotation is "supported" from a Talgent Computer that
// saves a rotated credential and answers the credential frame; any other
// value or none means it does not, and its credential is never rotated
// over the connection.
const HeaderCredentialRotation = "X-Talgent-Credential-Rotation"

// credentialFrame gives the machine the successor of its Computer
// Credential, which it saves before answering credential_saved. It is the
// only frame that carries a secret, and it is never logged.
type credentialFrame struct {
	Type            string `json:"type"`
	Credential      string `json:"credential"`
	ExpiresAtUnixMS int64  `json:"expires_at_unix_ms"`
}

// The machine's answers to a credential frame.
const (
	frameCredentialSaved    = "credential_saved"
	frameCredentialNotSaved = "credential_not_saved"
)

// credentialWatch keeps one connection's Computer Credential current: it
// checks it again every Revalidate, ends the connection once Computer
// refuses it, and rotates it when it is due and the machine supports
// rotation. One rotation is under way per connection at a time; a successor
// the machine did not save, or did not answer for within SuccessorWait, is
// issued again on a later check, since the machine still presents the
// current credential.
type credentialWatch struct {
	gateway    *Gateway
	conn       *websocket.Conn
	relay      *tunnel
	computerID string
	// rotates tells that the machine supports rotation; unsupportedLogged
	// that a due rotation it cannot take was logged.
	rotates           bool
	unsupportedLogged bool
	logger            *slog.Logger

	mu sync.Mutex
	// presented is the credential this connection holds now: the one it
	// authenticated with, or the successor its machine saved.
	presented string
	// successor is the credential issued and not yet saved or confirmed;
	// saved tells that the machine saved it.
	successor string
	saved     bool
	// issuedAt is when the successor was given to the machine.
	issuedAt time.Time
	answered chan struct{}
}

func newCredentialWatch(g *Gateway, conn *websocket.Conn, relay *tunnel, m machine, logger *slog.Logger) *credentialWatch {
	return &credentialWatch{gateway: g, conn: conn, relay: relay, computerID: m.computerID, rotates: m.rotates, presented: m.credential, logger: logger, answered: make(chan struct{}, 1)}
}

// run checks the credential until ctx ends; due tells whether the check
// made when the connection opened found it due for rotation.
func (w *credentialWatch) run(ctx context.Context, due bool) {
	ticker := time.NewTicker(w.gateway.revalidate)
	defer ticker.Stop()
	if due {
		w.rotate(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.answered:
			w.confirm(ctx)
		case <-ticker.C:
			w.confirm(ctx)
			w.expireUnanswered()
			if due, ok := w.check(ctx); ok && due {
				w.rotate(ctx)
			}
		}
	}
}

// answer takes the machine's answer to a credential frame; it reports
// whether the frame was one.
func (w *credentialWatch) answer(kind string) bool {
	if kind != frameCredentialSaved && kind != frameCredentialNotSaved {
		return false
	}
	w.mu.Lock()
	if w.successor != "" {
		if kind == frameCredentialSaved {
			w.saved = true
		} else {
			w.logger.Warn("computer gateway: the machine could not save its rotated credential")
			w.successor = ""
		}
	}
	w.mu.Unlock()
	select {
	case w.answered <- struct{}{}:
	default:
	}
	return true
}

// check authenticates the connection's credential again and ends the
// connection once Computer refuses it; it reports whether a rotation is
// due, and false for ok when Computer could not be asked. Only a refusal
// Computer answers ends the connection: an error, a control token it does
// not accept included, leaves the credential unchecked until the next
// check, so a platform fault never makes a machine give up its credential.
func (w *credentialWatch) check(ctx context.Context) (due, ok bool) {
	w.mu.Lock()
	presented, rotating := w.presented, w.successor != ""
	w.mu.Unlock()
	call, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	authenticated, err := w.gateway.computers.Authenticate(call, &computerprivatev1.AuthenticateRequest{Credential: presented})
	if err != nil {
		if ctx.Err() == nil {
			w.logger.WarnContext(ctx, "computer gateway: credential not checked", "error", err)
		}
		return false, false
	}
	switch authenticated.GetRefusal() {
	case computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_UNSPECIFIED:
		if authenticated.GetRotationDue() && !w.rotates && !w.unsupportedLogged {
			w.unsupportedLogged = true
			w.logger.WarnContext(ctx, "computer gateway: the machine's Talgent Computer cannot rotate its credential; it is refused once the credential expires unless it upgrades")
		}
		return w.rotates && authenticated.GetRotationDue() && !rotating, true
	case computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_COMPUTER_REMOVED:
		w.logger.InfoContext(ctx, "computer gateway: computer removed; ending its connection")
		_ = w.conn.Close(CloseComputerRemoved, "computer removed")
	case computerprivatev1.CredentialRefusal_CREDENTIAL_REFUSAL_REJECTED:
		w.logger.InfoContext(ctx, "computer gateway: credential revoked; ending its connection")
		_ = w.conn.Close(CloseCredentialRejected, "credential rejected")
	default:
		w.logger.WarnContext(ctx, "computer gateway: credential not checked", "refusal", authenticated.GetRefusal().String())
	}
	return false, false
}

// expireUnanswered forgets a successor the machine did not answer for
// within SuccessorWait, so a later check issues another.
func (w *credentialWatch) expireUnanswered() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.successor == "" || w.saved || w.gateway.now().Sub(w.issuedAt) < w.gateway.successorWait {
		return
	}
	w.logger.Warn("computer gateway: the machine did not answer its rotated credential; it is issued again")
	w.successor = ""
}

// rotate asks Computer for the successor of the connection's credential and
// gives it to the machine.
func (w *credentialWatch) rotate(ctx context.Context) {
	w.mu.Lock()
	presented, rotating := w.presented, w.successor != ""
	w.mu.Unlock()
	if rotating || !w.rotates {
		return
	}
	call, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	issued, err := w.gateway.computers.RotateCredential(call, &computerprivatev1.RotateCredentialRequest{ComputerId: w.computerID, Credential: presented})
	if err != nil {
		if ctx.Err() == nil {
			w.logger.WarnContext(ctx, "computer gateway: credential not rotated", "error", err)
		}
		return
	}
	w.mu.Lock()
	w.successor, w.saved, w.issuedAt = issued.GetCredential(), false, w.gateway.now()
	w.mu.Unlock()
	frame := credentialFrame{Type: "credential", Credential: issued.GetCredential(), ExpiresAtUnixMS: issued.GetExpiresAtUnixMs()}
	if err := w.relay.sendJSON(ctx, frame); err != nil {
		return
	}
	w.logger.InfoContext(ctx, "computer gateway: rotated credential sent to the machine")
}

// confirm confirms a successor the machine saved; one Computer could not
// confirm now is confirmed on the next check, and either credential
// authenticates until then.
func (w *credentialWatch) confirm(ctx context.Context) {
	w.mu.Lock()
	successor, saved := w.successor, w.saved
	w.mu.Unlock()
	if successor == "" || !saved {
		return
	}
	call, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if _, err := w.gateway.computers.ConfirmCredential(call, &computerprivatev1.ConfirmCredentialRequest{ComputerId: w.computerID, Credential: successor}); err != nil {
		if ctx.Err() == nil {
			w.logger.WarnContext(ctx, "computer gateway: rotated credential not confirmed", "error", err)
		}
		return
	}
	w.mu.Lock()
	w.presented, w.successor, w.saved = successor, "", false
	w.mu.Unlock()
	w.logger.InfoContext(ctx, "computer gateway: rotated credential confirmed")
}

// credentialAnswer reads the type of a machine frame that may answer a
// credential frame.
func credentialAnswer(data []byte) string {
	var frame struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &frame) != nil {
		return ""
	}
	return frame.Type
}
