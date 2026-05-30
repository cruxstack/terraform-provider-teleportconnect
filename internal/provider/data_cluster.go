package provider

import (
	"bytes"
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	tftypes "github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ datasource.DataSource              = (*dataCluster)(nil)
	_ datasource.DataSourceWithConfigure = (*dataCluster)(nil)
)

// dataCluster exposes cluster metadata: name, server version, and the TLS CA
// bundle (shared by every database, so callers can write it once and reuse it).
type dataCluster struct {
	pd *ProviderData
}

func newDataCluster() datasource.DataSource {
	return &dataCluster{}
}

type clusterDSModel struct {
	ResolvedID    tftypes.String `tfsdk:"id"`
	ClusterName   tftypes.String `tfsdk:"cluster_name"`
	ServerVersion tftypes.String `tfsdk:"server_version"`
	CACertificate tftypes.String `tfsdk:"ca_certificate"`
}

func (d *dataCluster) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster"
}

func (d *dataCluster) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		Description: "Exposes Teleport cluster metadata, including the cluster TLS CA bundle. The CA bundle is identical for every database in the cluster, so it can be written to a single file (for example to feed a database provider's `sslrootcert`) and reused across multiple database configurations.",
		Attributes: map[string]dsschema.Attribute{
			"id": dsschema.StringAttribute{
				Computed:    true,
				Description: "Same as cluster_name, exposed as `id` for consistency with Terraform convention.",
			},
			"cluster_name": dsschema.StringAttribute{
				Computed:    true,
				Description: "Name of the Teleport cluster the proxy belongs to.",
			},
			"server_version": dsschema.StringAttribute{
				Computed:    true,
				Description: "Teleport server version reported by the proxy.",
			},
			"ca_certificate": dsschema.StringAttribute{
				Computed:    true,
				Description: "PEM-encoded TLS CA bundle for the cluster. This is public trust material (not a secret); it is the CA that signs the proxy's serving certificate.",
			},
		},
	}
}

func (d *dataCluster) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected ProviderData type",
			fmt.Sprintf("Expected *provider.ProviderData, got %T", req.ProviderData))
		return
	}
	d.pd = pd
}

func (d *dataCluster) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.pd == nil || d.pd.Client == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider client is nil. This is a bug in the provider.")
		return
	}

	tflog.Debug(ctx, "reading teleport cluster metadata")

	pi, err := d.pd.Client.Ping(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to ping Teleport", err.Error())
		return
	}

	caResp, err := d.pd.Client.GetClusterCACert(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Failed to fetch cluster CA", err.Error())
		return
	}
	caPEM := caResp.TLSCA
	if !bytes.HasSuffix(caPEM, []byte("\n")) {
		caPEM = append(caPEM, '\n')
	}

	var data clusterDSModel
	data.ResolvedID = tftypes.StringValue(pi.ClusterName)
	data.ClusterName = tftypes.StringValue(pi.ClusterName)
	data.ServerVersion = tftypes.StringValue(pi.ServerVersion)
	data.CACertificate = tftypes.StringValue(string(caPEM))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
