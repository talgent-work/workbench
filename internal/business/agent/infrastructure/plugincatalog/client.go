package plugincatalog

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/public/v1/marketplacepublicv1connect"
	"github.com/talgent-work/workbench/internal/business/agent/application/access"
	"github.com/talgent-work/workbench/internal/business/agent/application/command"
	"github.com/talgent-work/workbench/internal/business/agent/domain"
	"github.com/talgent-work/workbench/internal/pkg/authn"
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

var _ command.PluginCatalogPort = (*Client)(nil)

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
		client:          marketplacepublicv1connect.NewMarketplaceServiceClient(&trustedClient, address),
		credentials:     marketplaceprivatev1connect.NewCredentialServiceClient(&trustedClient, address),
		selection:       marketplaceprivatev1connect.NewSelectionServiceClient(&trustedClient, address),
		credentialToken: config.CredentialToken,
	}, nil
}

func delegated[T any](ctx context.Context, user string, message *T) (*connect.Request[T], error) {
	if strings.TrimSpace(user) == "" {
		return nil, access.ErrAccessDenied
	}
	request := connect.NewRequest(message)
	// This is the existing internal service delegation contract. The public
	// gateway strips these headers; they are never copied from client input.
	request.Header().Set(authn.HeaderServiceID, "agent")
	request.Header().Set(authn.HeaderDelegatedUserID, user)
	propagation.TraceContext{}.Inject(ctx, propagation.HeaderCarrier(request.Header()))
	return request, nil
}

func (c *Client) ListPersonalPlugins(ctx context.Context, user string) ([]domain.PersonalPlugin, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var result []domain.PersonalPlugin
	cursor := ""
	for {
		request, err := delegated(ctx, user, &marketplaceprivatev1.ReadPersonalPluginsRequest{Cursor: cursor, PageSize: 100})
		if err != nil {
			return nil, err
		}
		request.Header().Set("Authorization", "Bearer "+c.credentialToken)
		response, err := c.selection.ReadPersonalPlugins(ctx, request)
		if err != nil {
			return nil, err
		}
		for _, record := range response.Msg.GetPlugins() {
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
		if response.Msg.GetNextCursor() == "" {
			return result, nil
		}
		if len(response.Msg.GetPlugins()) == 0 || response.Msg.GetNextCursor() != cursor {
			return nil, errors.New("agent: Marketplace returned an invalid personal page")
		}
	}
}

func (c *Client) ResolvePlugin(ctx context.Context, user, slug string) (domain.PluginRelease, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, err := delegated(ctx, user, &marketplaceprivatev1.ResolvePluginRequest{Slug: slug})
	if err != nil {
		return domain.PluginRelease{}, err
	}
	request.Header().Set("Authorization", "Bearer "+c.credentialToken)
	response, err := c.selection.ResolvePlugin(ctx, request)
	if err != nil {
		return domain.PluginRelease{}, err
	}
	release, err := pluginRelease(response.Msg.GetPlugin())
	if err != nil {
		return domain.PluginRelease{}, err
	}
	if release.Slug != slug {
		return domain.PluginRelease{}, errors.New("agent: Marketplace resolved a different plugin")
	}
	return release, nil
}
