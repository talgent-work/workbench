package storage

import (
	"errors"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	storageprivatev1 "github.com/talgent-work/contracts/gen/go/storage/private/v1"
)

// storageErrorDomain is the google.rpc.ErrorInfo domain in which Storage's
// private contracts name a refusal's reason.
const storageErrorDomain = "storage"

// refusalReason is the storage.private.v1.ErrorReason a Storage refusal names,
// or unspecified for any other error.
func refusalReason(err error) storageprivatev1.ErrorReason {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := connectproto.UnmarshalErrorDetail(detail)
		if info, ok := value.(*errdetails.ErrorInfo); valueErr == nil && ok && info.GetDomain() == storageErrorDomain {
			return storageprivatev1.ErrorReason(storageprivatev1.ErrorReason_value[info.GetReason()])
		}
	}
	return storageprivatev1.ErrorReason_ERROR_REASON_UNSPECIFIED
}
