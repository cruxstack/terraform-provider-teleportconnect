package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/gravitational/teleport/api/defaults"
	"github.com/gravitational/teleport/api/types"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	tftypes "github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var (
	_ datasource.DataSource              = (*dataNode)(nil)
	_ datasource.DataSourceWithConfigure = (*dataNode)(nil)
)

// dataNode looks up a Teleport SSH node by hostname and/or labels, like
// `tsh ls`.
type dataNode struct {
	pd *ProviderData
}

func newDataNode() datasource.DataSource {
	return &dataNode{}
}

type nodeDSModel struct {
	Hostname tftypes.String `tfsdk:"hostname"`
	Labels   tftypes.Map    `tfsdk:"labels"`

	ResolvedID      tftypes.String `tfsdk:"id"`
	MatchedHostname tftypes.String `tfsdk:"matched_hostname"`
	MatchedName     tftypes.String `tfsdk:"matched_name"`
	Addr            tftypes.String `tfsdk:"addr"`
	AllLabels       tftypes.Map    `tfsdk:"all_labels"`
}

func (d *dataNode) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_node"
}

func (d *dataNode) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		Description: "Looks up a Teleport SSH node by hostname and/or label match. Useful for resolving a `gateway_node` for teleportconnect_ssh_tunnel by labels instead of hardcoding hostnames.",
		Attributes: map[string]dsschema.Attribute{
			"hostname": dsschema.StringAttribute{
				Optional:    true,
				Description: "Exact hostname to match. At least one of `hostname` or `labels` must be set.",
			},
			"labels": dsschema.MapAttribute{
				Optional:    true,
				ElementType: tftypes.StringType,
				Description: "Map of labels (all must match exactly).",
			},

			"id": dsschema.StringAttribute{
				Computed:    true,
				Description: "Same as matched_name, exposed as `id` for consistency.",
			},
			"matched_name": dsschema.StringAttribute{
				Computed:    true,
				Description: "Teleport-internal node name (typically a UUID).",
			},
			"matched_hostname": dsschema.StringAttribute{
				Computed:    true,
				Description: "Hostname of the matched node.",
			},
			"addr": dsschema.StringAttribute{
				Computed:    true,
				Description: "Network address of the matched node.",
			},
			"all_labels": dsschema.MapAttribute{
				Computed:    true,
				ElementType: tftypes.StringType,
				Description: "All labels on the matched node.",
			},
		},
	}
}

func (d *dataNode) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *dataNode) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data nodeDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.pd == nil || d.pd.Client == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider client is nil. This is a bug in the provider.")
		return
	}

	wantHostname := data.Hostname.ValueString()
	wantLabels := map[string]string{}
	if !data.Labels.IsNull() && !data.Labels.IsUnknown() {
		resp.Diagnostics.Append(data.Labels.ElementsAs(ctx, &wantLabels, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if wantHostname == "" && len(wantLabels) == 0 {
		resp.Diagnostics.AddError("Missing required attribute",
			"At least one of `hostname` or `labels` must be set.")
		return
	}

	tflog.Debug(ctx, "looking up teleport node", map[string]any{
		"hostname": wantHostname,
		"labels":   wantLabels,
	})

	servers, err := d.pd.Client.GetNodes(ctx, defaults.Namespace)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list nodes", err.Error())
		return
	}

	var matches []types.Server
	for _, s := range servers {
		if wantHostname != "" && s.GetHostname() != wantHostname {
			continue
		}
		if !labelsMatch(s.GetAllLabels(), wantLabels) {
			continue
		}
		matches = append(matches, s)
	}

	switch len(matches) {
	case 0:
		resp.Diagnostics.AddError("No matching Teleport node",
			fmt.Sprintf("No node matched hostname=%q labels=%v.", wantHostname, wantLabels))
		return
	case 1:
	default:
		hostnames := make([]string, 0, len(matches))
		for _, m := range matches {
			hostnames = append(hostnames, m.GetHostname())
		}
		sort.Strings(hostnames)
		resp.Diagnostics.AddError("Ambiguous match",
			fmt.Sprintf("%d nodes matched: %v. Refine `hostname` or `labels`.", len(matches), hostnames))
		return
	}

	matched := matches[0]
	allLabels, diags := tftypes.MapValueFrom(ctx, tftypes.StringType, matched.GetAllLabels())
	resp.Diagnostics.Append(diags...)

	data.MatchedName = tftypes.StringValue(matched.GetName())
	data.ResolvedID = tftypes.StringValue(matched.GetName())
	data.MatchedHostname = tftypes.StringValue(matched.GetHostname())
	data.Addr = tftypes.StringValue(matched.GetAddr())
	data.AllLabels = allLabels

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
