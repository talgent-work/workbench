package plugincatalog

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	marketplaceprivatev1 "github.com/talgent-work/contracts/gen/go/marketplace/private/v1"
	"github.com/talgent-work/contracts/gen/go/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/contracts/gen/go/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
	"github.com/talgent-work/workbench/internal/pkg/otelsetup"
	"go.opentelemetry.io/otel/propagation"
)

type Config struct {
	CredentialToken string `json:"credential-token" yaml:"credential-token" toml:"credential-token"`
	Address         string `json:"address" yaml:"address" toml:"address"`
}

func (c Config) Validate() error {
	address, err := url.Parse(c.Address)
	if err != nil || address.Host == "" || (address.Scheme != "http" && address.Scheme != "https") || address.User != nil || address.RawQuery != "" || address.Fragment != "" {
		return errors.New("agent: a trusted internal Marketplace address is required")
	}
	return nil
}

type Client struct {
	client          marketplacepublicv1connect.MarketplaceServiceClient
	selection       marketplaceprivatev1connect.SelectionServiceClient
	credentials     marketplaceprivatev1connect.CredentialServiceClient
	credentialToken string
}

var _ command.MarketplacePort = (*Client)(nil)

func NewClient(httpClient *http.Client, config Config) (*Client, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	if httpClient == nil {
		return nil, errors.New("agent: Marketplace HTTP client is required")
	}
	// Preserve the shared transport and timeout without changing the caller's
	// client. Neither delegation headers nor credential material may be redirected.
	trustedClient := *httpClient
	trustedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	address := strings.TrimRight(config.Address, "/")
	return &Client{
		client:          marketplacepublicv1connect.NewMarketplaceServiceClient(connect.NewClient(connecthttp.NewTransport(&trustedClient, address, connecthttp.WithReadMaxBytes(0)))),
		credentials:     marketplaceprivatev1connect.NewCredentialServiceClient(connect.NewClient(connecthttp.NewTransport(&trustedClient, address, connecthttp.WithReadMaxBytes(0)))),
		selection:       marketplaceprivatev1connect.NewSelectionServiceClient(connect.NewClient(connecthttp.NewTransport(&trustedClient, address, connecthttp.WithReadMaxBytes(0)))),
		credentialToken: config.CredentialToken,
	}, nil
}

// delegated is the context of a call made for user under the internal
// service delegation contract, and the call's metadata.
func delegated(ctx context.Context, user string) (context.Context, *connect.CallInfo, error) {
	if strings.TrimSpace(user) == "" {
		return nil, nil, access.ErrAccessDenied
	}
	ctx, info := connect.NewClientContext(ctx)
	// This is the existing internal service delegation contract. The public
	// gateway strips these headers; they are never copied from client input.
	info.RequestHeader().Set(authn.HeaderServiceID, "agent")
	info.RequestHeader().Set(authn.HeaderDelegatedUserID, user)
	propagation.TraceContext{}.Inject(ctx, otelsetup.ConnectCarrier{Header: info.RequestHeader()})
	return ctx, info, nil
}

func (c *Client) ListPersonalPlugins(ctx context.Context, user string) ([]domain.PersonalPlugin, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var result []domain.PersonalPlugin
	cursor := ""
	for {
		ctx, info, err := delegated(ctx, user)
		if err != nil {
			return nil, err
		}
		info.RequestHeader().Set("Authorization", "Bearer "+c.credentialToken)
		response, err := c.selection.ReadPersonalPlugins(ctx, &marketplaceprivatev1.ReadPersonalPluginsRequest{Cursor: cursor, PageSize: 100})
		if err != nil {
			return nil, err
		}
		for _, record := range response.GetPlugins() {
			plugin, err := personalPlugin(record)
			if err != nil {
				return nil, err
			}
			if plugin.Slug <= cursor {
				return nil, errors.New("agent: Marketplace returned an invalid personal cursor")
			}
			cursor = plugin.Slug
			result = append(result, plugin)
		}
		if response.GetNextCursor() == "" {
			return result, nil
		}
		if len(response.GetPlugins()) == 0 || response.GetNextCursor() != cursor {
			return nil, errors.New("agent: Marketplace returned an invalid personal page")
		}
	}
}

func (c *Client) ResolvePlugin(ctx context.Context, user, slug string) (domain.PluginRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx, info, err := delegated(ctx, user)
	if err != nil {
		return domain.PluginRelease{}, err
	}
	info.RequestHeader().Set("Authorization", "Bearer "+c.credentialToken)
	response, err := c.selection.ResolvePlugin(ctx, &marketplaceprivatev1.ResolvePluginRequest{Slug: slug})
	if err != nil {
		return domain.PluginRelease{}, err
	}
	release, err := pluginRelease(response.GetPlugin())
	if err != nil {
		return domain.PluginRelease{}, err
	}
	if release.Slug != slug {
		return domain.PluginRelease{}, errors.New("agent: Marketplace resolved a different plugin")
	}
	return release, nil
}
