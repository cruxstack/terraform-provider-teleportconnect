package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	tftypes "github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/cruxstack/terraform-provider-teleportconnect/internal/sshcerts"
	"github.com/cruxstack/terraform-provider-teleportconnect/internal/tunnel"
)

// Compile-time interface assertions.
var (
	_ ephemeral.EphemeralResource              = (*ephemeralSSHTunnel)(nil)
	_ ephemeral.EphemeralResourceWithConfigure = (*ephemeralSSHTunnel)(nil)
	_ ephemeral.EphemeralResourceWithClose     = (*ephemeralSSHTunnel)(nil)
)

// ephemeralSSHTunnel opens a local TCP listener that proxies connections
// through a Teleport-managed SSH gateway node to an arbitrary host:port
// reachable from that gateway. This is the in-process equivalent of
// `tsh ssh -N -L LOCAL:TARGET_HOST:TARGET_PORT GATEWAY`.
//
// NOTE: schema and wiring are complete and the package builds, but this
// resource has not been smoke-tested against a live SSH gateway. The
// underlying tunnel/ssh.go layers proxy.NewClient -> ssh.NewClientConn ->
// direct-tcpip and may need real-world tuning around host-key
// verification edge cases.
type ephemeralSSHTunnel struct {
	pd *ProviderData
}

func newEphemeralSSHTunnel() ephemeral.EphemeralResource {
	return &ephemeralSSHTunnel{}
}

type sshTunnelModel struct {
	GatewayNode tftypes.String `tfsdk:"gateway_node"`
	SSHLogin    tftypes.String `tfsdk:"ssh_login"`
	TargetHost  tftypes.String `tfsdk:"target_host"`
	TargetPort  tftypes.Int64  `tfsdk:"target_port"`
	TTL         tftypes.String `tfsdk:"ttl"`
	Cluster     tftypes.String `tfsdk:"cluster"`

	LocalHost tftypes.String `tfsdk:"local_host"`
	LocalPort tftypes.Int64  `tfsdk:"local_port"`
}

func (e *ephemeralSSHTunnel) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_tunnel"
}

func (e *ephemeralSSHTunnel) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = ephschema.Schema{
		Description: "Opens a local TCP listener proxied through a Teleport-managed SSH gateway node to an arbitrary host:port reachable from that gateway. In-process equivalent of `tsh ssh -N -L LOCAL:TARGET_HOST:TARGET_PORT GATEWAY`.",
		Attributes: map[string]ephschema.Attribute{
			"gateway_node": ephschema.StringAttribute{
				Required:    true,
				Description: "Teleport node hostname (or UUID) used as the SSH jump host.",
			},
			"ssh_login": ephschema.StringAttribute{
				Required:    true,
				Description: "OS user to authenticate as on the gateway node (e.g. `root`, `ec2-user`).",
			},
			"target_host": ephschema.StringAttribute{
				Required:    true,
				Description: "Host the gateway node should forward traffic to (DNS name or IP).",
			},
			"target_port": ephschema.Int64Attribute{
				Required:    true,
				Description: "Port on target_host to forward traffic to.",
			},
			"ttl": ephschema.StringAttribute{
				Optional:    true,
				Description: "SSH certificate validity (Go duration). Defaults to 1h.",
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

func (e *ephemeralSSHTunnel) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
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

func (e *ephemeralSSHTunnel) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data sshTunnelModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if e.pd == nil || e.pd.Client == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider client is nil. This is a bug in the provider.")
		return
	}

	gateway := data.GatewayNode.ValueString()
	sshLogin := data.SSHLogin.ValueString()
	targetHost := data.TargetHost.ValueString()
	targetPort := int(data.TargetPort.ValueInt64())
	if gateway == "" || sshLogin == "" || targetHost == "" || targetPort == 0 {
		resp.Diagnostics.AddError("Missing required attribute", "gateway_node, ssh_login, target_host, and target_port are all required")
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
	cluster := stringOrDefault(data.Cluster.ValueString(), e.pd.Cluster)

	tflog.Info(ctx, "issuing ssh certificate", map[string]any{
		"gateway_node": gateway,
		"ssh_login":    sshLogin,
		"target":       fmt.Sprintf("%s:%d", targetHost, targetPort),
	})

	cred, err := sshcerts.Issue(ctx, e.pd.Client, sshcerts.Request{
		NodeName:       gateway,
		SSHLogin:       sshLogin,
		TTL:            ttl,
		RouteToCluster: cluster,
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to issue ssh credentials", err.Error())
		return
	}

	t, err := tunnel.NewSSHTunnel(context.Background(), tunnel.SSHOptions{
		ProxyAddress: e.pd.ProxyAddress,
		Cluster:      cluster,
		GatewayNode:  gateway,
		TargetHost:   targetHost,
		TargetPort:   targetPort,
		SSHLogin:     sshLogin,
		SSHCert:      cred.SSHCert,
		PrivateKey:   cred.PrivateKey,
		SSHCAs:       cred.SSHCAs,
		TLSConfig:    e.pd.Client.Config(),
		ALPNUpgrade:  tunnelUpgradeMode(e.pd.ALPNConnUpgrade),
	})
	if err != nil {
		resp.Diagnostics.AddError("Failed to start SSH tunnel", err.Error())
		return
	}

	id, err := e.pd.Tunnels.Add(t)
	if err != nil {
		_ = t.Close()
		resp.Diagnostics.AddError("Failed to register tunnel", err.Error())
		return
	}

	tflog.Info(ctx, "ssh tunnel ready", map[string]any{
		"local_host": t.LocalHost(),
		"local_port": t.LocalPort(),
		"tunnel_id":  id,
	})

	data.LocalHost = tftypes.StringValue(t.LocalHost())
	data.LocalPort = tftypes.Int64Value(int64(t.LocalPort()))
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		if got, ok := e.pd.Tunnels.Take(id); ok {
			_ = got.Close()
		}
		return
	}

	resp.Diagnostics.Append(resp.Private.SetKey(ctx, privateKeyTunnelID, jsonStr(id))...)
}

func (e *ephemeralSSHTunnel) Close(ctx context.Context, req ephemeral.CloseRequest, resp *ephemeral.CloseResponse) {
	if e.pd == nil || e.pd.Tunnels == nil || req.Private == nil {
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
		return
	}
	if err := t.Close(); err != nil {
		resp.Diagnostics.AddWarning("Tunnel close error", err.Error())
	}
	tflog.Info(ctx, "ssh tunnel closed", map[string]any{"tunnel_id": id})
}
