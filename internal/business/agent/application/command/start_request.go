package command

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/talgent-work/workbench/internal/business/agent/application/query"
)

var (
	ErrStartRequestCanceled = errors.New("agent: start request canceled")
	ErrStartRequestConflict = errors.New("agent: start request identity belongs to another operation")
)

// A request identity names everything its operation takes, so a repeat after
// a lost answer names the same Work and asks its Computer for the same Desk.
var (
	requestWorkNamespace = uuid.MustParse("2f8d4c1e-5b7a-4e0f-9c3d-6a1b8e2f7d40")
	requestDeskNamespace = uuid.MustParse("9b3e7a2c-1d4f-4a8b-b6e5-0c7d2f9a1e83")
)

// requestedWorkID is the identity of the Work a creation request establishes.
func requestedWorkID(requestID string) string {
	return uuid.NewSHA1(requestWorkNamespace, []byte(requestID)).String()
}

// requestedDeskID is the Desk a creation, Resume or Restart request asks its
// Work's Computer for.
func requestedDeskID(requestID string) string {
	return uuid.NewSHA1(requestDeskNamespace, []byte(requestID)).String()
}

// requestFingerprint is the plain content digest of a request (Request
// fingerprint): a repeat carrying other content is a conflict.
func requestFingerprint(content any) (string, error) {
	data, err := json.Marshal(content)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

// AnswerRetained decides how the retained record of a request identity
// answers a request carrying that identity; the first writer of the record
// decides it. It answers nil when the record admitted this very request, the
// cancellation when its caller cancelled it or a definitive refusal settled
// it, and a conflict when the identity names another operation, Work or
// content. A request with an empty WorkID is a creation accepting whichever
// Work its record names. Handlers apply it to the record they read, and the
// repository applies it again under the record's lock at commit.
func AnswerRetained(record, request query.StartRequest) error {
	creation := request.Kind == query.StartKindStart
	same := record.SameContent(request.Fingerprint)
	if record.Canceled {
		switch {
		case creation && record.WorkID == "" && same:
			return ErrWorkCreationCanceled
		case creation && record.WorkID == "":
			return ErrWorkCreationConflict
		case !creation && record.WorkID == request.WorkID:
			return ErrStartRequestCanceled
		}
		return ErrStartRequestConflict
	}
	if record.Kind != request.Kind || request.WorkID != "" && record.WorkID != request.WorkID {
		return ErrStartRequestConflict
	}
	if !same {
		if creation {
			return ErrWorkCreationConflict
		}
		return ErrStartRequestConflict
	}
	return nil
}

// findAdmitted reads the start a request identity admitted, if any.
func findAdmitted(ctx context.Context, requests query.StartRequestReader, requestID string) (query.AdmittedStart, bool, error) {
	record, found, err := requests.FindStartRequest(ctx, requestID)
	if err != nil || !found || !record.Admitted() {
		return query.AdmittedStart{}, false, err
	}
	return query.AdmittedStart{WorkID: record.WorkID, Kind: record.Kind}, true, nil
}
