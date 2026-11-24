package connectrpc

// A refusal of Storage's contracts names its reason
// (storage.private.v1.ErrorReason) in a google.rpc.ErrorInfo detail of
// ErrorDomain, so callers tell refusals with the same status code apart.

import (
	"errors"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
)

// ErrorDomain is the google.rpc.ErrorInfo domain of Storage's refusals.
const ErrorDomain = "storage"

// details hold one ErrorInfo detail per reason, built once: a registered
// message always serializes, so building them can only fail at start.
var details = func() map[storageprivatev1.ErrorReason]*connect.ErrorDetail {
	built := make(map[storageprivatev1.ErrorReason]*connect.ErrorDetail, len(storageprivatev1.ErrorReason_name))
	for value := range storageprivatev1.ErrorReason_name {
		reason := storageprivatev1.ErrorReason(value)
		detail, err := connectproto.NewErrorDetail(&errdetails.ErrorInfo{Domain: ErrorDomain, Reason: reason.String()})
		if err != nil {
			panic(err)
		}
		built[reason] = detail
	}
	return built
}()

// refusal is a refusal with its code and reason.
func refusal(code connect.Code, reason storageprivatev1.ErrorReason, err error) *connect.Error {
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
		if info, ok := value.(*errdetails.ErrorInfo); valueErr == nil && ok && info.GetDomain() == ErrorDomain {
			return storageprivatev1.ErrorReason(storageprivatev1.ErrorReason_value[info.GetReason()])
		}
	}
	return storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED
}
