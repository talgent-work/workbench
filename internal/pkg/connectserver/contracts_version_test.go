package connectserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/debug"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/emptypb"
)

var (
	unarySpec  = connect.Spec{StreamType: connect.StreamTypeUnary, Procedure: "/test.Service/Unary"}
	streamSpec = connect.Spec{StreamType: connect.StreamTypeServer, Procedure: "/test.Service/Stream"}
)

// serve mounts one unary and one server-streaming procedure behind the
// interceptor; each answers with fail's error when it is set.
func serve(t *testing.T, interceptor connect.ServerInterceptor, fail error) *connect.Client {
	t.Helper()
	server := connect.NewServer(interceptor)
	answer := func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
		if err := stream.Receive(&emptypb.Empty{}); err != nil {
			return err
		}
		if fail != nil {
			return fail
		}
		return stream.Send(&emptypb.Empty{})
	}
	server.Register(connect.Method{Spec: unarySpec, Handler: answer}, connect.Method{Spec: streamSpec, Handler: answer})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return connect.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL))
}

func callUnary(t *testing.T, client *connect.Client) (*connect.CallInfo, error) {
	t.Helper()
	ctx, info := connect.NewClientContext(context.Background())
	return info, client.CallUnary(ctx, unarySpec, &emptypb.Empty{}, &emptypb.Empty{})
}

func callStream(t *testing.T, client *connect.Client) (*connect.CallInfo, error) {
	t.Helper()
	ctx, info := connect.NewClientContext(context.Background())
	stream, err := client.CallServerStream(ctx, streamSpec, &emptypb.Empty{})
	if err != nil {
		return info, err
	}
	defer stream.Close()
	for {
		if err := stream.Receive(&emptypb.Empty{}); err != nil {
			if errors.Is(err, io.EOF) {
				return info, nil
			}
			return info, err
		}
	}
}

// Intent (real): a consumer checks the contracts release the Workbench was
// built with on any response, so every answer carries it: unary and
// streaming, success and refusal alike.
func TestContractsVersionInterceptorAnswersEveryResponseWithTheVersion(t *testing.T) {
	refused := connect.NewError(connect.CodePermissionDenied, "refused")
	for _, tc := range []struct {
		name string
		call func(*testing.T, *connect.Client) (*connect.CallInfo, error)
		fail error
	}{
		{name: "unary success", call: callUnary},
		{name: "unary refusal", call: callUnary, fail: refused},
		{name: "stream success", call: callStream},
		{name: "stream refusal", call: callStream, fail: refused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := serve(t, NewContractsVersionInterceptor("v0.4.2"), tc.fail)
			info, err := tc.call(t, client)
			if (err != nil) != (tc.fail != nil) {
				t.Fatalf("call error = %v, want failure %v", err, tc.fail != nil)
			}
			got := info.ResponseHeader().Get("talgent-contracts-version")
			if got == "" {
				got = info.ResponseTrailer().Get("talgent-contracts-version")
			}
			if got != "v0.4.2" {
				t.Fatalf("talgent-contracts-version = %q, want v0.4.2", got)
			}
		})
	}
}

// Intent (real): a build that names no contracts release claims none, so a
// consumer treats it as incompatible instead of trusting a made-up version.
func TestContractsVersionInterceptorSendsNoHeaderWithoutARelease(t *testing.T) {
	client := serve(t, NewContractsVersionInterceptor(""), nil)
	info, err := callUnary(t, client)
	if err != nil {
		t.Fatalf("CallUnary: %v", err)
	}
	if info.ResponseHeader().Has("talgent-contracts-version") || info.ResponseTrailer().Has("talgent-contracts-version") {
		t.Fatalf("talgent-contracts-version sent without a release: %q", info.ResponseHeader().Get("talgent-contracts-version"))
	}
}

// Intent (real): the version is the release tag of the contracts module the
// binary was built with, as its module information names it; a pseudo-version,
// a local replacement or a missing module names no release.
func TestContractsVersionReadsTheBuiltContractsRelease(t *testing.T) {
	contracts := func(version string, replace *debug.Module) *debug.BuildInfo {
		return &debug.BuildInfo{Deps: []*debug.Module{
			{Path: "connectrpc.com/connect/v2", Version: "v2.0.2"},
			{Path: "github.com/talgent-work/contracts", Version: version, Replace: replace},
		}}
	}
	for _, tc := range []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{name: "release", info: contracts("v0.4.2", nil), want: "v0.4.2"},
		{name: "pseudo-version", info: contracts("v0.4.3-0.20261107000000-abcdefabcdef", nil), want: ""},
		{name: "pre-release", info: contracts("v0.5.0-rc.1", nil), want: ""},
		{name: "replaced by a release", info: contracts("v0.4.2", &debug.Module{Path: "github.com/talgent-work/contracts", Version: "v0.4.5"}), want: "v0.4.5"},
		{name: "replaced by a directory", info: contracts("v0.4.2", &debug.Module{Path: "../contracts"}), want: ""},
		{name: "absent", info: &debug.BuildInfo{}, want: ""},
		{name: "no build information", info: nil, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContractsVersion(tc.info); got != tc.want {
				t.Fatalf("ContractsVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

// Intent (real): a listener that answers some requests itself, such as a
// native API refusing a caller or reporting its identity, names the version
// on those answers too, and a Connect answer behind it carries it once.
func TestContractsVersionHandlerAnswersEveryResponseOnce(t *testing.T) {
	server := connect.NewServer(NewContractsVersionInterceptor("v1.2.3"))
	server.Register(connect.Method{Spec: unarySpec, Handler: func(_ context.Context, _ connect.Spec, stream connect.ServerStream) error {
		if err := stream.Receive(&emptypb.Empty{}); err != nil {
			return err
		}
		return stream.Send(&emptypb.Empty{})
	}})
	connectMux := http.NewServeMux()
	connecthttp.Mount(connectMux, server)
	mux := http.NewServeMux()
	mux.Handle(unarySpec.Procedure, connectMux)
	mux.HandleFunc("/identity", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "{}") })
	mux.HandleFunc("/refused", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	httpServer := httptest.NewServer(NewContractsVersionHandler("v1.2.3", mux))
	t.Cleanup(httpServer.Close)

	for _, path := range []string{"/identity", "/refused", "/unknown"} {
		response, err := httpServer.Client().Post(httpServer.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if got := response.Header.Values(ContractsVersionHeader); len(got) != 1 || got[0] != "v1.2.3" {
			t.Fatalf("%s answered with %s %q, want one v1.2.3", path, ContractsVersionHeader, got)
		}
	}
	client := connect.NewClient(connecthttp.NewTransport(httpServer.Client(), httpServer.URL))
	info, err := callUnary(t, client)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.ResponseHeader().Values(ContractsVersionHeader); len(got) != 1 || got[0] != "v1.2.3" {
		t.Fatalf("Connect answered with %s %q, want one v1.2.3", ContractsVersionHeader, got)
	}
}

// A build that names no contracts release sends no header.
func TestContractsVersionHandlerWithoutAVersionSendsNone(t *testing.T) {
	recorder := httptest.NewRecorder()
	NewContractsVersionHandler("", http.NotFoundHandler()).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/x", nil))
	if got := recorder.Header().Values(ContractsVersionHeader); len(got) != 0 {
		t.Fatalf("answered with %s %q, want none", ContractsVersionHeader, got)
	}
}
