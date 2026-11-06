package querytest

import "context"

// NoRequiredResponse reports that no required interaction awaits a reply.
func NoRequiredResponse(context.Context, string) (bool, error) {
	return false, nil
}
