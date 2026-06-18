// Package provider implements the cruxstack/teleportconnect Terraform
// provider. It exposes Teleport-mediated access to remote resources
// (database credentials, database tunnels, SSH tunnels) without requiring
// tsh/jq/bash on the runner.
package provider

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/gravitational/teleport/api/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/cruxstack/terraform-provider-teleportconnect/internal/auth"
)

var (
	_ provider.Provider                       = (*teleportconnectProvider)(nil)
	_ provider.ProviderWithEphemeralResources = (*teleportconnectProvider)(nil)
)

type teleportconnectProvider struct {
	version string // set at build time, surfaced in user-agent strings
}

// New returns a constructor compatible with providerserver.Serve.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &teleportconnectProvider{version: version}
	}
}

// ALPNConnUpgradeMode controls whether tunnels do an HTTPS upgrade before TLS
// routing. Some L7 LBs (e.g. AWS ALB) terminate TLS with their own cert and
// fool the upstream auto-probe, so we expose an explicit override.
type ALPNConnUpgradeMode int

const (
	ALPNAuto ALPNConnUpgradeMode = iota
	ALPNYes
	ALPNNo
)

// ProviderData is shared with every resource, data source, and ephemeral
// resource via the framework's *Data fields. It only caches configuration at
// Configure time; the Teleport connection is established lazily on first use
// via Client, so an unused provider block performs no network or file I/O at
// plan time (see issue #17).
type ProviderData struct {
	ProxyAddress    string
	Cluster         string // leaf-cluster override for cert RouteToCluster; may be empty
	ALPNConnUpgrade ALPNConnUpgradeMode
	Insecure        bool
	// Tunnels tracks listeners that must outlive a single Open call, until
	// Close fires or the plugin process exits.
	Tunnels *TunnelRegistry

	// authCfg is the resolved auth configuration used to build the client.
	authCfg auth.Config

	// mu guards the lazy-connect state below. The first Client call dials
	// (or joins) and pings; the result - client, clusterName, or error - is
	// cached so later resources reuse it and a failed connect is not retried.
	mu          sync.Mutex
	connected   bool
	client      *client.Client
	clusterName string // proxy's own cluster, resolved from Ping (SSH DialHost fallback)
	connErr     error
}

// Client returns the authenticated Teleport client, connecting on first call
// and caching the result. A failed connect is cached too, so an unreachable
// proxy is not redialed by every resource in the same run.
func (pd *ProviderData) Client(ctx context.Context) (*client.Client, error) {
	pd.mu.Lock()
	defer pd.mu.Unlock()
	if pd.connected {
		return pd.client, pd.connErr
	}
	pd.connected = true

	tflog.Info(ctx, "connecting to teleport proxy", map[string]any{
		"proxy_address":     pd.authCfg.ProxyAddress,
		"cluster":           pd.authCfg.Cluster,
		"use_local_profile": pd.authCfg.UseLocalProfile,
		"identity_file":     pd.authCfg.IdentityFilePath != "" || pd.authCfg.IdentityFileData != "",
		"join_method":       pd.authCfg.JoinMethod,
	})

	clt, err := auth.Build(ctx, pd.authCfg)
	if err != nil {
		pd.connErr = fmt.Errorf("failed to connect to Teleport: %w", err)
		return nil, pd.connErr
	}

	// Ping confirms the credentials work and resolves the proxy's own
	// cluster name (the SSH DialHost fallback).
	pi, err := clt.Ping(ctx)
	if err != nil {
		_ = clt.Close()
		pd.connErr = fmt.Errorf("teleport ping failed: %w", err)
		return nil, pd.connErr
	}
	tflog.Info(ctx, "teleport connection established", map[string]any{
		"cluster_name":   pi.ClusterName,
		"server_version": pi.ServerVersion,
	})

	pd.client = clt
	pd.clusterName = pi.ClusterName
	return pd.client, nil
}

// ClusterName ensures the client is connected and returns the proxy's own
// cluster name, used as the SSH DialHost cluster fallback.
func (pd *ProviderData) ClusterName(ctx context.Context) (string, error) {
	if _, err := pd.Client(ctx); err != nil {
		return "", err
	}
	pd.mu.Lock()
	defer pd.mu.Unlock()
	return pd.clusterName, nil
}

// Close tears down the cached client if one was established.
func (pd *ProviderData) Close() error {
	pd.mu.Lock()
	defer pd.mu.Unlock()
	if pd.client != nil {
		return pd.client.Close()
	}
	return nil
}

// providerModel mirrors the HCL schema. types.* fields distinguish unset from
// empty when validating mutually exclusive auth modes.
type providerModel struct {
	ProxyAddress        types.String `tfsdk:"proxy_address"`
	Cluster             types.String `tfsdk:"cluster"`
	IdentityFilePath    types.String `tfsdk:"identity_file_path"`
	IdentityFileData    types.String `tfsdk:"identity_file_data"`
	UseLocalProfile     types.Bool   `tfsdk:"use_local_profile"`
	JoinMethod          types.String `tfsdk:"join_method"`
	JoinToken           types.String `tfsdk:"join_token"`
	JoinAudience        types.String `tfsdk:"join_audience"`
	Insecure            types.Bool   `tfsdk:"insecure"`
	ALPNConnUpgrade     types.String `tfsdk:"alpn_conn_upgrade"`
	JoinALPNConnUpgrade types.String `tfsdk:"join_alpn_conn_upgrade"`
	AuthALPNConnUpgrade types.String `tfsdk:"auth_alpn_conn_upgrade"`
	EagerConnect        types.Bool   `tfsdk:"eager_connect"`
}

func (p *teleportconnectProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "teleportconnect"
	resp.Version = p.version
}

func (p *teleportconnectProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Teleport-mediated access to remote resources for Terraform without host-installed tsh/jq.",
		Attributes: map[string]schema.Attribute{
			"proxy_address": schema.StringAttribute{
				Required:    true,
				Description: "Teleport Proxy Service address (e.g. teleport.example.com:443).",
			},
			"cluster": schema.StringAttribute{
				Optional:    true,
				Description: "Optional leaf cluster name for trusted-cluster routing. Defaults to the cluster the proxy belongs to.",
			},
			"identity_file_path": schema.StringAttribute{
				Optional:    true,
				Description: "Path to an identity file produced by `tctl auth sign` or `tbot`. Mutually exclusive with other auth modes.",
			},
			"identity_file_data": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "Inline identity file contents (PEM bundle). Mutually exclusive with other auth modes.",
			},
			"use_local_profile": schema.BoolAttribute{
				Optional:    true,
				Description: "If true, reuse the local ~/.tsh profile for the configured proxy_address. Mirrors the developer's `tsh login` session.",
			},
			"join_method": schema.StringAttribute{
				Optional:    true,
				Description: "Delegated Machine ID join method for CI. The provider fetches the platform's OIDC/JWT identity token and joins the cluster in-process (no identity file or tbot sidecar). One of: github, gitlab, kubernetes, spacelift. Requires join_token. Mutually exclusive with the other auth modes.",
			},
			"join_token": schema.StringAttribute{
				Optional:    true,
				Description: "Name of the Teleport join token to use with join_method.",
			},
			"join_audience": schema.StringAttribute{
				Optional:    true,
				Description: "Expected audience claim of the identity token for join_method. Defaults to the proxy host. For GitHub this is requested explicitly; for other platforms it must match how the token is minted.",
			},
			"insecure": schema.BoolAttribute{
				Optional:    true,
				Description: "Skip verification of the proxy TLS certificate (equivalent to `tsh --insecure`). Should never be true in production.",
			},
			"alpn_conn_upgrade": schema.StringAttribute{
				Optional:    true,
				Description: "Whether to perform an HTTPS connection upgrade for ALPN tunnels. Set to 'yes' when the Teleport proxy sits behind an L7 load balancer (AWS ALB, etc.) that terminates TLS with a public cert. 'no' to force direct TLS routing. 'auto' (default) probes the proxy and decides; the probe is unreliable for some LBs - prefer 'yes' if you know your proxy is fronted by one.",
			},
			"join_alpn_conn_upgrade": schema.StringAttribute{
				Optional:    true,
				Description: "HTTPS connection upgrade behavior for the delegated-join handshake (the unauthenticated dial to the proxy's JoinService), independent of alpn_conn_upgrade (tunnels) and auth_alpn_conn_upgrade (post-join). Defaults to 'auto'. Set to 'no' when the proxy is behind an L4 load balancer with a private endpoint, where forcing the upgrade makes the join dial verify the proxy's resolved private IP and fail with a no-IP-SANs error.",
			},
			"auth_alpn_conn_upgrade": schema.StringAttribute{
				Optional:    true,
				Description: "HTTPS connection upgrade behavior for the post-join auth client (the authenticated API connection made after a successful join), independent of alpn_conn_upgrade (tunnels) and join_alpn_conn_upgrade. Defaults to 'auto'. Set to 'yes' when the auth connection must be ALPN-routed through the proxy (otherwise it can terminate against the internal auth certificate). The join handshake and the post-join auth dial can require opposite values on some topologies.",
			},
			"eager_connect": schema.BoolAttribute{
				Optional:    true,
				Description: "If true, connect to and ping the Teleport proxy during provider configuration (legacy behavior, fails fast at configure time). Defaults to false, where the connection is established lazily on first resource/data-source use, so an unused provider block (e.g. a count = 0 ephemeral) does not require cluster reachability at plan time.",
			},
		},
	}
}

// Configure validates config, builds an authenticated Teleport client, and
// stashes a *ProviderData for resources and data sources to reach.
func (p *teleportconnectProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Unknown at plan time: fail clearly rather than dialing a zero value.
	if cfg.ProxyAddress.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("proxy_address"),
			"Unknown proxy_address",
			"proxy_address must be known at configure time. Avoid sourcing it from a resource that hasn't been applied yet.",
		)
		return
	}

	proxyAddress := strings.TrimSpace(cfg.ProxyAddress.ValueString())
	if _, _, err := net.SplitHostPort(proxyAddress); err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("proxy_address"),
			"Invalid proxy_address",
			fmt.Sprintf("proxy_address must be in host:port form (e.g. teleport.example.com:443): %v", err),
		)
		return
	}

	upgradeMode, err := parseALPNUpgradeMode(cfg.ALPNConnUpgrade.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("alpn_conn_upgrade"),
			"Invalid alpn_conn_upgrade value",
			err.Error(),
		)
		return
	}

	// The join handshake and post-join auth dials each have their own upgrade
	// knob, independent of the tunnel's and of each other: some topologies
	// (L4 LB + private endpoint) need opposite values for the two dials.
	joinUpgradeMode, err := parseALPNUpgradeMode(cfg.JoinALPNConnUpgrade.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("join_alpn_conn_upgrade"),
			"Invalid join_alpn_conn_upgrade value",
			err.Error(),
		)
		return
	}

	authUpgradeModeVal, err := parseALPNUpgradeMode(cfg.AuthALPNConnUpgrade.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("auth_alpn_conn_upgrade"),
			"Invalid auth_alpn_conn_upgrade value",
			err.Error(),
		)
		return
	}

	authCfg := auth.Config{
		ProxyAddress:     proxyAddress,
		Cluster:          cfg.Cluster.ValueString(),
		IdentityFilePath: cfg.IdentityFilePath.ValueString(),
		IdentityFileData: cfg.IdentityFileData.ValueString(),
		UseLocalProfile:  cfg.UseLocalProfile.ValueBool(),
		JoinMethod:       cfg.JoinMethod.ValueString(),
		JoinToken:        cfg.JoinToken.ValueString(),
		JoinAudience:     cfg.JoinAudience.ValueString(),
		JoinALPNUpgrade:  authUpgradeMode(joinUpgradeMode),
		AuthALPNUpgrade:  authUpgradeMode(authUpgradeModeVal),
		Insecure:         cfg.Insecure.ValueBool(),
	}

	if err := authCfg.Validate(); err != nil {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
		return
	}

	// The connection is established lazily on first use (see ProviderData.Client)
	// so an unused provider block performs no I/O at plan time.
	pd := &ProviderData{
		ProxyAddress:    authCfg.ProxyAddress,
		Cluster:         authCfg.Cluster,
		ALPNConnUpgrade: upgradeMode,
		Insecure:        authCfg.Insecure,
		Tunnels:         NewTunnelRegistry(),
		authCfg:         authCfg,
	}

	// eager_connect restores the legacy behavior of dialing and pinging at
	// configure time, failing fast before any resource runs.
	if cfg.EagerConnect.ValueBool() {
		if _, err := pd.Client(ctx); err != nil {
			resp.Diagnostics.AddError("Failed to connect to Teleport", err.Error())
			return
		}
	}

	resp.ResourceData = pd
	resp.DataSourceData = pd
	resp.EphemeralResourceData = pd
}

func parseALPNUpgradeMode(v string) (ALPNConnUpgradeMode, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "auto":
		return ALPNAuto, nil
	case "yes", "true", "on", "required":
		return ALPNYes, nil
	case "no", "false", "off", "never":
		return ALPNNo, nil
	default:
		return ALPNAuto, fmt.Errorf("expected one of auto/yes/no, got %q", v)
	}
}

func (p *teleportconnectProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}

func (p *teleportconnectProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newDataCluster,
		newDataDatabase,
		newDataNode,
	}
}

func (p *teleportconnectProvider) EphemeralResources(_ context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{
		newEphemeralDBCertificate,
		newEphemeralDBTunnel,
		newEphemeralSSHTunnel,
	}
}
