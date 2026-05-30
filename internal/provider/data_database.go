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
	_ datasource.DataSource              = (*dataDatabase)(nil)
	_ datasource.DataSourceWithConfigure = (*dataDatabase)(nil)
)

// dataDatabase looks up a Teleport database by name and/or labels, like
// `tsh db ls`.
type dataDatabase struct {
	pd *ProviderData
}

func newDataDatabase() datasource.DataSource {
	return &dataDatabase{}
}

type databaseDSModel struct {
	Name   tftypes.String `tfsdk:"name"`
	Labels tftypes.Map    `tfsdk:"labels"`

	Protocol    tftypes.String `tfsdk:"protocol"`
	URI         tftypes.String `tfsdk:"uri"`
	AllLabels   tftypes.Map    `tfsdk:"all_labels"`
	ResolvedID  tftypes.String `tfsdk:"id"`
	MatchedName tftypes.String `tfsdk:"matched_name"`
}

func (d *dataDatabase) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_database"
}

func (d *dataDatabase) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dsschema.Schema{
		Description: "Looks up a Teleport database resource by exact name and/or label match. Useful for feeding the resolved database name into a teleportconnect_db_certificate or teleportconnect_db_tunnel resource without hardcoding it.",
		Attributes: map[string]dsschema.Attribute{
			"name": dsschema.StringAttribute{
				Optional:    true,
				Description: "Exact Teleport database service name to match. At least one of `name` or `labels` must be set.",
			},
			"labels": dsschema.MapAttribute{
				Optional:    true,
				ElementType: tftypes.StringType,
				Description: "Map of labels (all must match exactly). At least one of `name` or `labels` must be set.",
			},

			"id": dsschema.StringAttribute{
				Computed:    true,
				Description: "Same as matched_name, exposed as `id` for consistency with Terraform convention.",
			},
			"matched_name": dsschema.StringAttribute{
				Computed:    true,
				Description: "Resolved Teleport database service name.",
			},
			"protocol": dsschema.StringAttribute{
				Computed:    true,
				Description: "Database protocol (postgres, mysql, etc.).",
			},
			"uri": dsschema.StringAttribute{
				Computed:    true,
				Description: "Backing database endpoint URI as registered in Teleport.",
			},
			"all_labels": dsschema.MapAttribute{
				Computed:    true,
				ElementType: tftypes.StringType,
				Description: "All labels on the matched database resource.",
			},
		},
	}
}

func (d *dataDatabase) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *dataDatabase) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data databaseDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if d.pd == nil || d.pd.Client == nil {
		resp.Diagnostics.AddError("Provider not configured", "Provider client is nil. This is a bug in the provider.")
		return
	}

	wantName := data.Name.ValueString()
	wantLabels := map[string]string{}
	if !data.Labels.IsNull() && !data.Labels.IsUnknown() {
		resp.Diagnostics.Append(data.Labels.ElementsAs(ctx, &wantLabels, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if wantName == "" && len(wantLabels) == 0 {
		resp.Diagnostics.AddError("Missing required attribute",
			"At least one of `name` or `labels` must be set.")
		return
	}

	// GetDatabaseServers (db_server heartbeats), not GetDatabases: most roles
	// can read db_server but not db, matching `tsh db ls`. Dedupe by name.
	tflog.Debug(ctx, "looking up teleport database", map[string]any{
		"name":   wantName,
		"labels": wantLabels,
	})

	servers, err := d.pd.Client.GetDatabaseServers(ctx, defaults.Namespace)
	if err != nil {
		resp.Diagnostics.AddError("Failed to list database servers", err.Error())
		return
	}

	seen := map[string]struct{}{}
	var matches []types.Database
	for _, s := range servers {
		db := s.GetDatabase()
		if db == nil {
			continue
		}
		if _, dup := seen[db.GetName()]; dup {
			continue
		}
		if wantName != "" && db.GetName() != wantName {
			continue
		}
		if !labelsMatch(db.GetAllLabels(), wantLabels) {
			continue
		}
		seen[db.GetName()] = struct{}{}
		matches = append(matches, db)
	}

	switch len(matches) {
	case 0:
		resp.Diagnostics.AddError("No matching Teleport database",
			fmt.Sprintf("No database matched name=%q labels=%v.", wantName, wantLabels))
		return
	case 1:
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.GetName())
		}
		sort.Strings(names)
		resp.Diagnostics.AddError("Ambiguous match",
			fmt.Sprintf("%d databases matched: %v. Refine `name` or `labels`.", len(matches), names))
		return
	}

	matched := matches[0]
	allLabels, diags := tftypes.MapValueFrom(ctx, tftypes.StringType, matched.GetAllLabels())
	resp.Diagnostics.Append(diags...)

	data.MatchedName = tftypes.StringValue(matched.GetName())
	data.ResolvedID = tftypes.StringValue(matched.GetName())
	data.Protocol = tftypes.StringValue(matched.GetProtocol())
	data.URI = tftypes.StringValue(matched.GetURI())
	data.AllLabels = allLabels

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
