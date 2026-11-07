// Package storageerror reads and writes the reason Storage's private
// contracts give a refusal (storage.private.v1.ErrorReason), so callers tell
// refusals with the same status code apart.
package storageerror

import (
	"errors"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	storageprivatev1 "github.com/talgent-work/contracts/gen/storage/private/v1"
)

// Domain is the google.rpc.ErrorInfo domain of Storage's refusals.
const Domain = "storage"

// details hold one ErrorInfo detail per reason, built once: a registered
// message always serializes, so building them can only fail at start.
var details = func() map[storageprivatev1.ErrorReason]*connect.ErrorDetail {
	built := make(map[storageprivatev1.ErrorReason]*connect.ErrorDetail, len(storageprivatev1.ErrorReason_name))
	for value := range storageprivatev1.ErrorReason_name {
		reason := storageprivatev1.ErrorReason(value)
		detail, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Domain: Domain, Reason: reason.String()})
		if err != nil {
			panic(err)
		}
		built[reason] = detail
	}
	return built
}()

// New is a refusal with its code and reason.
func New(code connect.Code, reason storageprivatev1.ErrorReason, err error) *connect.Error {
	refused := connect.NewError(code, err.Error()).WithCause(err)
	if detail, ok := details[reason]; ok {
		refused = refused.WithDetail(detail)
	}
	return refused
}

// ReasonOf is the reason of a Storage refusal, or unspecified.
func ReasonOf(err error) storageprivatev1.ErrorReason {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := connectproto.UnmarshalErrorDetail(detail)
		if info, ok := value.(*errdetails.ErrorInfo); valueErr == nil && ok && info.GetDomain() == Domain {
			return storageprivatev1.ErrorReason(storageprivatev1.ErrorReason_value[info.GetReason()])
		}
	}
	return storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED
}
