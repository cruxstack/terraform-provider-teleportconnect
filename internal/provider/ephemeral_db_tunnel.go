package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	tftypes "github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/cruxstack/terraform-provider-teleportconnect/internal/dbcerts"
	"github.com/cruxstack/terraform-provider-teleportconnect/internal/tunnel"
)

// jsonStr round-trips a string through JSON encode/decode for use with the
// framework's private state, which requires JSON-encoded values.
func jsonStr(s string) []byte {
	b, _ := json.Marshal(s)
	return b
}

func unjsonStr(b []byte) (string, error) {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return "", err
	}
	return s, nil
}

// Compile-time interface assertions.
var (
	_ ephemeral.EphemeralResource              = (*ephemeralDBTunnel)(nil)
	_ ephemeral.EphemeralResourceWithConfigure = (*ephemeralDBTunnel)(nil)
	_ ephemeral.EphemeralResourceWithClose     = (*ephemeralDBTunnel)(nil)
)

// privateKeyTunnelID names the private-state slot used to round-trip the
// tunnel registry ID from Open to Close. JSON-encoded string.
const privateKeyTunnelID = "tunnel_id"

// ephemeralDBTunnel opens a local TCP listener that proxies connections
// through the Teleport proxy via TLS routing. Downstream providers
// (postgresql, mysql, etc.) connect to localhost:<local_port> as if they
// were talking to the database directly; the tunnel handles all the
// Teleport authentication and ALPN routing transparently.
//
// This is the in-process equivalent of `tsh proxy db --tunnel`.
type ephemeralDBTunnel struct {
	pd *ProviderData
}

func newEphemeralDBTunnel() ephemeral.EphemeralResource {
	return &ephemeralDBTunnel{}
}

type dbTunnelModel struct {
	Database tftypes.String `tfsdk:"database"`
	Protocol tftypes.String `tfsdk:"protocol"`
	DBUser   tftypes.String `tfsdk:"db_user"`
	DBName   tftypes.String `tfsdk:"db_name"`
	TTL      tftypes.String `tfsdk:"ttl"`
	Cluster  tftypes.String `tfsdk:"cluster"`

	LocalHost tftypes.String `tfsdk:"local_host"`
	LocalPort tftypes.Int64  `tfsdk:"local_port"`
}

func (e *ephemeralDBTunnel) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_db_tunnel"
}

func (e *ephemeralDBTunnel) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = ephschema.Schema{
		Description: "Opens a local TCP listener proxied to a Teleport-protected database via the proxy's TLS routing. Downstream database providers connect to local_host:local_port without TLS or client certs; the tunnel handles all Teleport authentication. This is the in-process equivalent of `tsh proxy db --tunnel`.",
		Attributes: map[string]ephschema.Attribute{
			"database": ephschema.StringAttribute{
				Required:    true,
				Description: "Teleport database service name (matches `tsh db ls`).",
			},
			"protocol": ephschema.StringAttribute{
				Optional:    true,
				Description: "Database protocol (postgres, mysql, etc.). When omitted, looked up from the Teleport database resource.",
			},
			"db_user": ephschema.StringAttribute{
				Optional:    true,
				Description: "Database user to embed in the issued certificate.",
			},
			"db_name": ephschema.StringAttribute{
				Optional:    true,
				Description: "Database name to embed in the issued certificate.",
			},
			"ttl": ephschema.StringAttribute{
				Optional:    true,
				Description: "Certificate validity (Go duration). Defaults to 1h.",
			},
			"cluster": ephschema.StringAttribute{
				Optional:    true,
				Description: "Override route_to_cluster (leaf cluster name).",
			},

			"local_host": ephschema.StringAttribute{
				Computed:    true,
				Description: "Localhost address the tunnel is listening on (typically 127.0.0.1).",
			},
			"local_port": ephschema.Int64Attribute{
				Computed:    true,
				Description: "OS-assigned local port the tunnel is listening on.",
			},
		},
	}
}

func (e *ephemeralDBTunnel) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected ProviderData type",
			fmt.Sprintf("Expected *provider.ProviderData, got %T", req.ProviderData),
		)
		return
	}
	e.pd = pd
}

func (e *ephemeralDBTunnel) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data dbTunnelModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if e.pd == nil || e.pd.Client == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider client is nil. This is a bug in the provider.")
		return
	}

	dbName := data.Database.ValueString()
	if dbName == "" {
		resp.Diagnostics.AddError("Missing required attribute", "database is required")
		return
	}

	ttl := time.Duration(0)
	if v := data.TTL.ValueString(); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			resp.Diagnostics.AddError("Invalid ttl", err.Error())
			return
		}
		ttl = d
	}

	tflog.Info(ctx, "opening database tunnel", map[string]any{
		"database": dbName,
		"protocol": data.Protocol.ValueString(),
		"db_user":  data.DBUser.ValueString(),
		"db_name":  data.DBName.ValueString(),
	})

	// Issue a fresh database cert. The tunnel TLS-handshakes to the proxy
	// using this cert, which carries the RouteToDatabase routing claim.
	cred, err := dbcerts.Issue(ctx, e.pd.Client, dbcerts.Request{
		Database:       dbName,
		Protocol:       data.Protocol.ValueString(),
		DBUser:         data.DBUser.ValueString(),
		DBName:         data.DBName.ValueString(),
		TTL:            ttl,
		RouteToCluster: stringOrDefault(data.Cluster.ValueString(), e.pd.Cluster),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to issue database credentials", err.Error())
		return
	}

	// We use context.Background as the tunnel's parent because the tunnel
	// must outlive the Open RPC. The provider's Close handler (or
	// CloseAll on shutdown) is what tears it down.
	t, err := tunnel.NewDBTunnel(context.Background(), tunnel.DBOptions{
		ProxyAddress:  e.pd.ProxyAddress,
		Protocol:      cred.Protocol,
		ClientCertPEM: cred.CertPEM,
		ClientKeyPEM:  cred.KeyPEM,
		CAPEM:         cred.CAPEM,
		ALPNUpgrade:   tunnelUpgradeMode(e.pd.ALPNConnUpgrade),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to start local tunnel", err.Error())
		return
	}

	id, err := e.pd.Tunnels.Add(t)
	if err != nil {
		_ = t.Close()
		resp.Diagnostics.AddError("Failed to register tunnel", err.Error())
		return
	}

	tflog.Info(ctx, "database tunnel ready", map[string]any{
		"local_host": t.LocalHost(),
		"local_port": t.LocalPort(),
		"tunnel_id":  id,
	})

	data.LocalHost = tftypes.StringValue(t.LocalHost())
	data.LocalPort = tftypes.Int64Value(int64(t.LocalPort()))
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		// Set failed: avoid leaking the tunnel.
		if got, ok := e.pd.Tunnels.Take(id); ok {
			_ = got.Close()
		}
		return
	}

	// Stash the registry ID so Close can find this tunnel. The framework
	// requires private state values to be valid JSON.
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateKeyTunnelID, jsonStr(id))...)
}

func (e *ephemeralDBTunnel) Close(ctx context.Context, req ephemeral.CloseRequest, resp *ephemeral.CloseResponse) {
	if e.pd == nil || e.pd.Tunnels == nil {
		return
	}
	if req.Private == nil {
		return
	}
	raw, diags := req.Private.GetKey(ctx, privateKeyTunnelID)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || len(raw) == 0 {
		return
	}
	id, err := unjsonStr(raw)
	if err != nil {
		resp.Diagnostics.AddError("Failed to decode private state", err.Error())
		return
	}
	t, ok := e.pd.Tunnels.Take(id)
	if !ok {
		// Already closed or never registered; nothing to do.
		return
	}
	if err := t.Close(); err != nil {
		resp.Diagnostics.AddWarning("Tunnel close error", err.Error())
	}
	tflog.Info(ctx, "database tunnel closed", map[string]any{"tunnel_id": id})
}
