package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/samber/oops"

	"github.com/talgent-work/workbench/internal/business/agent/application/command"
)

// A Work on a Managed Computer has the files of its located inputs as
// current read-only copies under <work root>/inputs, as a Cloud Work has them
// mounted at /workspace/inputs (Managed layout). Agent hands the machine the
// whole list with a fresh Access Link for each, which Application obtained
// from Storage.
//
// Syncing the inputs is its own step, never a Required Startup Step: the
// first delivery attempts it once and a failure there is only logged, so a
// start never waits for or fails on it. The execution keeps its own due
// marker (inputs_due): a new Managed execution starts due, a File Change
// Published Fact for its located inputs adds to it, and a sync clears only the
// marks it read, so a change during a sync stays due. The recovery sweep
// finds a due execution and syncs it again, counting each failed attempt
// (inputs_sync_failures); the attempt that reaches domain.InputSyncAttempts
// records why the sync was abandoned (inputs_sync_abandoned), and nothing
// syncs again until a later change. The located inputs Directory,
// recorded with the execution when it begins (inputs_directory), lets a
// change find its executions by an indexed query.
//
// A manifest's Access Links expire (storage.link-ttl), and the machine stops
// retrying a download within that lifetime, so a copy it could not fetch
// would stay stale without another change. Each successful sync records when
// it happened (inputs_synced_at), and the recovery sweep marks the inputs of
// a live execution with located inputs due again once that is older than
// the refresh interval; the machine skips the files it already holds.

// inputDelivery is an authority whose Works receive their inputs as copies
// the platform hands their machine.
type inputDelivery interface {
	// syncInputs hands the machine the current inputs of the execution's
	// Work; the execution records the changes it covered.
	syncInputs(ctx context.Context, record executionRecord, inputs command.ManagedInputs) error
	// refresh is how long synced input copies stay current.
	refresh() time.Duration
}

var _ inputDelivery = ManagedEnvironment{}

// inputManifest is the body of the relay's inputs PUT. Its links are read
// grants: it is never logged, recorded or echoed.
type inputManifest struct {
	Files []inputManifestFile `json:"files"`
}

type inputManifestFile struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	UpdatedAt string `json:"updated_at"`
	URL       string `json:"url"`
}

// deliverInputs replaces the Work's input manifest on its machine. Each link
// is the Access Link's grant on the Gateway's content address, which the
// machine downloads with the Work's identity.
func (m *ManagedComputers) deliverInputs(ctx context.Context, spec managedSpecification, workID string, attachments []command.InputFile) error {
	manifest := inputManifest{Files: make([]inputManifestFile, 0, len(attachments))}
	for _, attachment := range attachments {
		link, err := m.contentLink(attachment.AccessPath)
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, inputManifestFile{Name: attachment.Name, Size: attachment.SizeBytes, UpdatedAt: attachment.UpdatedAt.UTC().Format(time.RFC3339Nano), URL: link})
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return oops.Wrap(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, m.endpoint(spec, workID)+"/inputs", bytes.NewReader(body))
	if err != nil {
		return oops.Wrap(err)
	}
	request.Header.Set("X-Talgent-Control-Token", m.token)
	request.Header.Set("Content-Type", "application/json")
	return m.do(ctx, request, "deliver_work_inputs")
}

// contentLink carries an Access Link's grant to the content address.
func (m *ManagedComputers) contentLink(accessPath string) (string, error) {
	link, err := url.Parse(accessPath)
	if err != nil || link.RawQuery == "" || link.Host != "" {
		// The path itself is a grant; it is never echoed.
		return "", errors.New("agent: Storage issued an Access Link without a grant")
	}
	return m.content + "?" + link.RawQuery, nil
}

// syncInputs hands the machine the current files of the Work's located
// inputs; a Work whose layout located none has none to copy.
func (m ManagedEnvironment) syncInputs(ctx context.Context, record executionRecord, inputs command.ManagedInputs) error {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return err
	}
	if !inputs.Located {
		return nil
	}
	return m.machines.deliverInputs(ctx, spec, record.SessionID, inputs.Files)
}

func (m ManagedEnvironment) refresh() time.Duration { return m.inputsRefresh }
