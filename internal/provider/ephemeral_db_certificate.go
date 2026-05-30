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

var (
	_ ephemeral.EphemeralResource              = (*ephemeralDBCertificate)(nil)
	_ ephemeral.EphemeralResourceWithConfigure = (*ephemeralDBCertificate)(nil)
)

// ephemeralDBCertificate issues a short-lived TLS client cert for a Teleport
// database plus the proxy host:port and trust bundle to connect via TLS
// routing: the in-process equivalent of `tsh db login` + `tsh db config`.
type ephemeralDBCertificate struct {
	pd *ProviderData
}

func newEphemeralDBCertificate() ephemeral.EphemeralResource {
	return &ephemeralDBCertificate{}
}

type dbCertificateModel struct {
	Database tftypes.String `tfsdk:"database"`
	Protocol tftypes.String `tfsdk:"protocol"`
	DBUser   tftypes.String `tfsdk:"db_user"`
	DBName   tftypes.String `tfsdk:"db_name"`
	TTL      tftypes.String `tfsdk:"ttl"`
	Cluster  tftypes.String `tfsdk:"cluster"`

	Host          tftypes.String `tfsdk:"host"`
	Port          tftypes.Int64  `tfsdk:"port"`
	CACertificate tftypes.String `tfsdk:"ca_certificate"`
	Certificate   tftypes.String `tfsdk:"certificate"`
	PrivateKey    tftypes.String `tfsdk:"private_key"`
}

func (e *ephemeralDBCertificate) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_db_certificate"
}

func (e *ephemeralDBCertificate) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = ephschema.Schema{
		Description: "Issues a short-lived TLS client certificate for a Teleport-protected database. The certificate/private_key/ca_certificate can be fed into a downstream database provider (postgresql, mysql, etc.) to connect through the Teleport proxy via TLS routing.",
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
			"ca_certificate": ephschema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "PEM-encoded TLS CA bundle for verifying the proxy.",
			},
			"certificate": ephschema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "PEM-encoded TLS client certificate.",
			},
			"private_key": ephschema.StringAttribute{
				Computed:    true,
				Sensitive:   true,
				Description: "PEM-encoded TLS client private key.",
			},
		},
	}
}

func (e *ephemeralDBCertificate) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
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

func (e *ephemeralDBCertificate) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data dbCertificateModel
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
		resp.Diagnostics.AddError("Failed to issue database certificate", err.Error())
		return
	}

	host, port, err := proxyHostPort(e.pd.ProxyAddress)
	if err != nil {
		resp.Diagnostics.AddError("Invalid proxy_address", err.Error())
		return
	}

	data.Host = tftypes.StringValue(host)
	data.Port = tftypes.Int64Value(int64(port))
	data.CACertificate = tftypes.StringValue(string(res.CAPEM))
	data.Certificate = tftypes.StringValue(string(res.CertPEM))
	data.PrivateKey = tftypes.StringValue(string(res.KeyPEM))

	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
