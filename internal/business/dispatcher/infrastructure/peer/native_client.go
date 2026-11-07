package peer

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/talgent-work/contracts/gen/dispatcher/private/v1/dispatcherprivatev1connect"
)

// NativeClients supplies outbound private execution clients for peer routing.
// The shared HTTP transport owns connection pooling and cancellation.
type NativeClients struct{ http *http.Client }

func NewNativeClients(client *http.Client) *NativeClients { return &NativeClients{http: client} }
func (c *NativeClients) ForAddress(address string) dispatcherprivatev1connect.ExecutionServiceClient {
	return dispatcherprivatev1connect.NewExecutionServiceClient(connect.NewClient(connecthttp.NewTransport(c.http, "http://"+address, connecthttp.WithReadMaxBytes(0))))
}
