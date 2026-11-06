package authn

import (
	"net/http"
	"strings"
)

// HeaderReaderAdmission carries a trusted internal service's statement that
// the delegated user is within the value of a named binding reference, as
// "reference=value", such as "project=<id>". The gateway strips it from
// external requests, and it is honored only on a delegated service call.
const HeaderReaderAdmission = "x-talgent-reader-admission"

// ReaderAdmission returns the admission of a delegated service call; a
// request a client sent directly carries none.
func ReaderAdmission(header http.Header) (reference, value string, ok bool) {
	if strings.TrimSpace(header.Get(HeaderServiceID)) == "" || strings.TrimSpace(header.Get(HeaderDelegatedUserID)) == "" {
		return "", "", false
	}
	reference, value, found := strings.Cut(strings.TrimSpace(header.Get(HeaderReaderAdmission)), "=")
	if !found || reference == "" || value == "" {
		return "", "", false
	}
	return reference, value, true
}

// SetReaderAdmission states a delegated reader's admission on an outbound call.
func SetReaderAdmission(header http.Header, reference, value string) {
	header.Set(HeaderReaderAdmission, reference+"="+value)
}
