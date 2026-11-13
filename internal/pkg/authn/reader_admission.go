package authn

import (
	"strings"

	"connectrpc.com/connect/v2"
)

// HeaderReaderAdmission carries a trusted internal service's statement that
// the delegated user is a member of a Work Caller of an extension-defined
// kind, as "<kind>:<id>", such as "project:<id>". The gateway strips it from
// external requests, and it is honored only on a delegated service call.
const HeaderReaderAdmission = "x-talgent-reader-admission"

// ReaderAdmission returns the Work Caller kind and identity a delegated
// service call admits its user within; a request a client sent directly, or
// one whose admission is not "<kind>:<id>", carries none.
func ReaderAdmission(header *connect.Header) (kind, id string, ok bool) {
	if strings.TrimSpace(header.Get(HeaderServiceID)) == "" || strings.TrimSpace(header.Get(HeaderDelegatedUserID)) == "" {
		return "", "", false
	}
	caller, valid := parseWorkFact(header.Get(HeaderReaderAdmission))
	if !valid || caller == (WorkFact{}) {
		return "", "", false
	}
	return caller.Kind, caller.ID, true
}
