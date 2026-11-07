package connectrpc

import (
	"context"

	"connectrpc.com/connect/v2"
)

// requestHeader is the metadata of the call ctx serves; a call made outside
// Connect carries none.
func requestHeader(ctx context.Context) *connect.Header {
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		return info.RequestHeader()
	}
	return new(connect.Header)
}
