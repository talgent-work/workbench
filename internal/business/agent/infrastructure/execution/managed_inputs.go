package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/samber/oops"
)

// A Work on a Managed Computer has its input source's Attachments as current
// read-only copies under <work root>/inputs, as a Cloud Work has them mounted
// at /workspace/inputs. Agent hands the machine the whole list with a fresh
// Access Link for each.
//
// Syncing the inputs is its own step, never a Required Startup Step: the
// first delivery attempts it once and a failure there is only logged, so a
// start never waits for or fails on it. The allocation keeps its own due
// marker (inputs_due): a new Managed allocation starts due, a File Change
// Published Fact for the input source's File Space adds to it, and a sync
// clears only the marks it read, so a change during a sync stays due. The
// recovery sweep finds a due allocation and syncs it again. The input
// source's File Space owner, recorded by the sync before it lists, lets a
// change find its allocations by an indexed query.
//
// A manifest's Access Links expire (storage.link-ttl), and the machine stops
// retrying a download within that lifetime, so a copy it could not fetch
// would stay stale without another change. Each successful sync records when
// it happened (inputs_synced_at), and the recovery sweep marks the inputs of
// a live allocation with an input source due again once that is older than
// the refresh interval; the machine skips the files it already holds.

// WorkInputFiles is Storage's File Space Contract as the Managed realization
// reads a Work's input source. Agent keeps the source opaque.
type WorkInputFiles interface {
	// InputOwner names the File Space owner of the input source; empty when
	// it names none.
	InputOwner(ctx context.Context, source string) (string, error)
	// InputAttachments lists the Attachments of owner's File Space, each with
	// a fresh Access Link the Work downloads it through.
	InputAttachments(ctx context.Context, owner string) ([]InputAttachment, error)
}

// InputAttachment is one Attachment and the path of its Access Link, as
// Storage issues it for the platform's public address.
type InputAttachment struct {
	Name       string
	SizeBytes  int64
	UpdatedAt  time.Time
	AccessPath string
}

// inputDelivery is an authority whose Works receive their inputs as copies
// the platform hands their machine.
type inputDelivery interface {
	// syncInputs hands the machine the current inputs of the execution's
	// Work; the execution records the changes it covered.
	syncInputs(ctx context.Context, record allocationRecord) error
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
func (m *ManagedComputers) deliverInputs(ctx context.Context, spec managedSpecification, workID string, attachments []InputAttachment) error {
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

// syncInputs records the File Space owner of the Work's input source, then
// hands the machine that space's current Attachments; a Work without an input
// source has none to copy. The owner is recorded before the listing, so a
// change after it is either listed or marks the execution due again.
func (m ManagedEnvironment) syncInputs(ctx context.Context, record allocationRecord) error {
	spec, err := decodeManagedSpecification(record)
	if err != nil {
		return err
	}
	admitted, err := decodeStartRequest(record.RequestJSON)
	if err != nil {
		return oops.With("operation", "decode_native_request").Wrap(err)
	}
	if source := strings.TrimSpace(admitted.InputSource.Reference); source != "" {
		owner, err := m.inputs.InputOwner(ctx, source)
		if err != nil {
			return oops.With("operation", "resolve_input_owner", "work_id", record.SessionID).Wrap(err)
		}
		if owner == "" {
			return oops.With("work_id", record.SessionID).Errorf("agent: the Work's input source names no File Space")
		}
		if err := m.recordInputOwner(ctx, record, owner); err != nil {
			return err
		}
		attachments, err := m.inputs.InputAttachments(ctx, owner)
		if err != nil {
			return oops.With("operation", "list_work_inputs", "work_id", record.SessionID).Wrap(err)
		}
		if err := m.machines.deliverInputs(ctx, spec, record.SessionID, attachments); err != nil {
			return err
		}
	}
	return nil
}

func (m ManagedEnvironment) refresh() time.Duration { return m.inputsRefresh }

func (m ManagedEnvironment) recordInputOwner(ctx context.Context, record allocationRecord, owner string) error {
	if record.InputOwner == owner {
		return nil
	}
	if _, err := m.records.db.Context(ctx).Exec(`UPDATE execution_allocation SET input_owner = ? WHERE id = ?`, owner, record.ID); err != nil {
		return oops.With("operation", "record_input_owner").Wrap(err)
	}
	return nil
}
