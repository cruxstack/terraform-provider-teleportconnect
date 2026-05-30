package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	tftypes "github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/cruxstack/terraform-provider-teleportconnect/internal/dbcerts"
)

// Compile-time interface assertions.
var (
	_ ephemeral.EphemeralResource              = (*ephemeralDBCredentials)(nil)
	_ ephemeral.EphemeralResourceWithConfigure = (*ephemeralDBCredentials)(nil)
)

// ephemeralDBCredentials issues a short-lived TLS client certificate for a
// Teleport-protected database, plus the proxy host:port and trust bundle
// needed to connect through the proxy via TLS routing.
//
// This is the Go-native equivalent of `tsh db login` + `tsh db config`,
// without writing anything to disk and without requiring tsh on the runner.
type ephemeralDBCredentials struct {
	pd *ProviderData
}

func newEphemeralDBCredentials() ephemeral.EphemeralResource {
	return &ephemeralDBCredentials{}
}

type dbCredentialsModel struct {
	Database tftypes.String `tfsdk:"database"`
	Protocol tftypes.String `tfsdk:"protocol"`
	DBUser   tftypes.String `tfsdk:"db_user"`
	DBName   tftypes.String `tfsdk:"db_name"`
	TTL      tftypes.String `tfsdk:"ttl"`
	Cluster  tftypes.String `tfsdk:"cluster"`

	Host tftypes.String `tfsdk:"host"`
	Port tftypes.Int64  `tfsdk:"port"`
	CA   tftypes.String `tfsdk:"ca"`
	Cert tftypes.String `tfsdk:"cert"`
	Key  tftypes.String `tfsdk:"key"`
}

func (e *ephemeralDBCredentials) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_db_credentials"
}

func (e *ephemeralDBCredentials) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = ephschema.Schema{
		Description: "Issues a short-lived database client certificate for a Teleport-protected database. The cert/key/CA can be fed into a downstream database provider (postgresql, mysql, etc.) to connect through the Teleport proxy via TLS routing.",
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
				Description: "Database user to embed in the issued certificate (e.g. `dbviewer`).",
			},
			"db_name": ephschema.StringAttribute{
				Optional:    true,
				Description: "Database name to embed in the issued certificate.",
			},
			"ttl": ephschema.StringAttribute{
				Optional:    true,
				Description: "Certificate validity (Go duration, e.g. `1h`, `30m`). Defaults to 1h.",
			},
			"cluster": ephschema.StringAttribute{
				Optional:    true,
				Description: "Override route_to_cluster (leaf cluster name).",
			},

			"host": ephschema.StringAttribute{
				Computed:    true,
				Description: "Proxy hostname to connect to.",
			},
			"port": ephschema.Int64Attribute{
				Computed:    true,
				Description: "Proxy port to connect to.",
			},
			"ca": ephschema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "PEM-encoded TLS CA bundle for verifying the proxy.",
			},
			"cert": ephschema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "PEM-encoded TLS client certificate.",
			},
			"key": ephschema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "PEM-encoded TLS client private key.",
			},
		},
	}
}

func (e *ephemeralDBCredentials) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
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

func (e *ephemeralDBCredentials) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data dbCredentialsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if e.pd == nil || e.pd.Client == nil {
		resp.Diagnostics.AddError(
			"Provider not configured",
			"Provider client is nil. This is a bug in the provider.",
		)
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

	tflog.Info(ctx, "issuing database certificate", map[string]any{
		"database": dbName,
		"protocol": data.Protocol.ValueString(),
		"db_user":  data.DBUser.ValueString(),
		"db_name":  data.DBName.ValueString(),
	})

	res, err := dbcerts.Issue(ctx, e.pd.Client, dbcerts.Request{
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

	host, port, err := proxyHostPort(e.pd.ProxyAddress)
	if err != nil {
		resp.Diagnostics.AddError("Invalid proxy_address", err.Error())
		return
	}

	data.Host = tftypes.StringValue(host)
	data.Port = tftypes.Int64Value(int64(port))
	data.CA = tftypes.StringValue(string(res.CAPEM))
	data.Cert = tftypes.StringValue(string(res.CertPEM))
	data.Key = tftypes.StringValue(string(res.KeyPEM))

	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
