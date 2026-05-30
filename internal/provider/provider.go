// Package provider implements the cruxstack/teleportconnect Terraform
// provider. It exposes Teleport-mediated access to remote resources
// (database credentials, database tunnels, SSH tunnels) without requiring
// tsh/jq/bash on the runner.
package provider

import (
	"context"
	"fmt"
	"strings"

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

// Compile-time interface assertions.
var (
	_ provider.Provider                       = (*teleportconnectProvider)(nil)
	_ provider.ProviderWithEphemeralResources = (*teleportconnectProvider)(nil)
)

// teleportconnectProvider is the root provider type. The version field is set
// at build time via main and surfaced in user-agent strings.
type teleportconnectProvider struct {
	version string
}

// New returns a constructor compatible with providerserver.Serve.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &teleportconnectProvider{version: version}
	}
}

// ALPNConnUpgradeMode controls whether tunnels do an HTTPS upgrade dance
// before TLS routing. Some L7 load balancers (e.g. AWS ALB) negotiate ALPN
// values back to the client but still terminate TLS with their own cert,
// which fools direct TLS-routing. The "auto" probe in the upstream
// teleport API (`IsALPNConnUpgradeRequired`) is documented as unreliable
// for many real-world setups, so we expose an explicit override.
type ALPNConnUpgradeMode int

const (
	ALPNAuto ALPNConnUpgradeMode = iota
	ALPNYes
	ALPNNo
)

// ProviderData is the value stashed into ResourceData/EphemeralResourceData/
// DataSourceData. Resources call .Client to get the connected *client.Client
// and .ProxyAddress for tunnel/ALPN bootstrapping. Tunnels stores active
// local listeners keyed by an opaque ID so Close handlers can tear them
// down deterministically.
type ProviderData struct {
	Client          *client.Client
	ProxyAddress    string
	Cluster         string
	ALPNConnUpgrade ALPNConnUpgradeMode

	// Tunnels is shared across ephemeral resources to track listeners that
	// must outlive a single Open call (the listener goroutine has to live
	// until Close fires or the provider plugin process exits).
	Tunnels *TunnelRegistry
}

// providerModel mirrors the HCL schema. Optional fields use types.* so we can
// distinguish unset from empty when validating mutually exclusive auth modes.
type providerModel struct {
	ProxyAddress       types.String `tfsdk:"proxy_address"`
	Cluster            types.String `tfsdk:"cluster"`
	IdentityFilePath   types.String `tfsdk:"identity_file_path"`
	IdentityFileData   types.String `tfsdk:"identity_file_data"`
	UseLocalProfile    types.Bool   `tfsdk:"use_local_profile"`
	JoinMethod         types.String `tfsdk:"join_method"`
	JoinToken          types.String `tfsdk:"join_token"`
	InsecureSkipVerify types.Bool   `tfsdk:"insecure_skip_verify"`
	ALPNConnUpgrade    types.String `tfsdk:"alpn_conn_upgrade"`
}

func (p *teleportconnectProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	// TypeName drives resource prefixes and the default local alias in
	// required_providers. Keep this in sync with the registry source.
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
				Description: "Delegated Machine ID join method (e.g. iam, github, gcp, spacelift, kubernetes). Requires join_token.",
			},
			"join_token": schema.StringAttribute{
				Optional:    true,
				Description: "Bot join token name. Requires join_method.",
			},
			"insecure_skip_verify": schema.BoolAttribute{
				Optional:    true,
				Description: "Skip verification of the proxy TLS certificate. Should never be true in production.",
			},
			"alpn_conn_upgrade": schema.StringAttribute{
				Optional:    true,
				Description: "Whether to perform an HTTPS connection upgrade for ALPN tunnels. Set to 'yes' when the Teleport proxy sits behind an L7 load balancer (AWS ALB, etc.) that terminates TLS with a public cert. 'no' to force direct TLS routing. 'auto' (default) probes the proxy and decides; the probe is unreliable for some LBs - prefer 'yes' if you know your proxy is fronted by one.",
			},
		},
	}
}

// Configure parses provider config, validates that exactly one auth method
// is set, builds an authenticated Teleport API client, and stashes a
// *ProviderData on the response so resources/data sources can reach it.
func (p *teleportconnectProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// During plan when the value is unknown we cannot proceed; surface a
	// clear error rather than passing zero-values into auth.Build.
	if cfg.ProxyAddress.IsUnknown() {
		resp.Diagnostics.AddAttributeError(
			path.Root("proxy_address"),
			"Unknown proxy_address",
			"proxy_address must be known at configure time. Avoid sourcing it from a resource that hasn't been applied yet.",
		)
		return
	}

	authCfg := auth.Config{
		ProxyAddress:       cfg.ProxyAddress.ValueString(),
		Cluster:            cfg.Cluster.ValueString(),
		IdentityFilePath:   cfg.IdentityFilePath.ValueString(),
		IdentityFileData:   cfg.IdentityFileData.ValueString(),
		UseLocalProfile:    cfg.UseLocalProfile.ValueBool(),
		JoinMethod:         cfg.JoinMethod.ValueString(),
		JoinToken:          cfg.JoinToken.ValueString(),
		InsecureSkipVerify: cfg.InsecureSkipVerify.ValueBool(),
	}

	if err := authCfg.Validate(); err != nil {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
		return
	}

	tflog.Info(ctx, "connecting to teleport proxy", map[string]any{
		"proxy_address":     authCfg.ProxyAddress,
		"cluster":           authCfg.Cluster,
		"use_local_profile": authCfg.UseLocalProfile,
		"identity_file":     authCfg.IdentityFilePath != "" || authCfg.IdentityFileData != "",
	})

	clt, err := auth.Build(ctx, authCfg)
	if err != nil {
		resp.Diagnostics.AddError("Failed to connect to Teleport", err.Error())
		return
	}

	upgradeMode := ALPNAuto
	switch strings.ToLower(strings.TrimSpace(cfg.ALPNConnUpgrade.ValueString())) {
	case "", "auto":
		upgradeMode = ALPNAuto
	case "yes", "true", "on", "required":
		upgradeMode = ALPNYes
	case "no", "false", "off", "never":
		upgradeMode = ALPNNo
	default:
		resp.Diagnostics.AddAttributeError(
			path.Root("alpn_conn_upgrade"),
			"Invalid alpn_conn_upgrade value",
			fmt.Sprintf("expected one of auto/yes/no, got %q", cfg.ALPNConnUpgrade.ValueString()),
		)
		return
	}

	pd := &ProviderData{
		Client:          clt,
		ProxyAddress:    authCfg.ProxyAddress,
		Cluster:         authCfg.Cluster,
		ALPNConnUpgrade: upgradeMode,
		Tunnels:         NewTunnelRegistry(),
	}

	// Hand the same handle to all three surfaces so resources, data sources,
	// and ephemeral resources share the connection.
	resp.ResourceData = pd
	resp.DataSourceData = pd
	resp.EphemeralResourceData = pd

	// Sanity ping: confirm the connection is actually authenticated by
	// fetching cluster info. Failures here surface bad credentials at
	// configure time rather than during a later resource read.
	if pi, err := clt.Ping(ctx); err != nil {
		resp.Diagnostics.AddError(
			"Teleport ping failed",
			fmt.Sprintf("client.Ping returned: %v", err),
		)
		return
	} else {
		tflog.Info(ctx, "teleport connection established", map[string]any{
			"cluster_name":   pi.ClusterName,
			"server_version": pi.ServerVersion,
		})
	}
}

func (p *teleportconnectProvider) Resources(_ context.Context) []func() resource.Resource {
	return nil
}

func (p *teleportconnectProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newDataDatabase,
		newDataNode,
	}
}

func (p *teleportconnectProvider) EphemeralResources(_ context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{
		newEphemeralDBCredentials,
		newEphemeralDBTunnel,
		newEphemeralSSHTunnel,
	}
}
