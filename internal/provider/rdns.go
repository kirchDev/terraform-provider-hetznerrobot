package provider

import (
	"context"
	"net/http"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

var (
	_ resource.Resource                = (*rdnsResource)(nil)
	_ resource.ResourceWithImportState = (*rdnsResource)(nil)
)

// NewRDNSResource manages the reverse DNS entry of one IP address.
func NewRDNSResource() resource.Resource { return &rdnsResource{} }

type rdnsResource struct{ resourceBase }

type apiRDNS struct {
	IP  string `json:"ip"`
	PTR string `json:"ptr"`
}

type rdnsModel struct {
	IP  types.String `tfsdk:"ip"`
	PTR types.String `tfsdk:"ptr"`
}

func (m *rdnsModel) fill(e apiRDNS) {
	m.IP = types.StringValue(e.IP)
	m.PTR = types.StringValue(e.PTR)
}

func rdnsPath(ip string) string { return "/rdns/" + escape(ip) }

func (r *rdnsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rdns"
}

func (r *rdnsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The reverse DNS (PTR) entry of an IPv4 address or of an address inside an IPv6 subnet. " +
			"Creating it overwrites an entry that already exists; destroying it deletes the entry.",
		Attributes: map[string]schema.Attribute{
			"ip":  reqStrNew("IP address the entry belongs to."),
			"ptr": reqStr("PTR record, e.g. `server1.example.com`."),
		},
	}
}

// write creates or updates the entry. POST /rdns/{ip} is an upsert (201 on
// create, 200 on update), so create and update share it.
func (r *rdnsResource) write(ctx context.Context, plan *rdnsModel) error {
	got, err := client.SendObject[apiRDNS](ctx, r.client, http.MethodPost, rdnsPath(plan.IP.ValueString()), "rdns",
		url.Values{"ptr": {plan.PTR.ValueString()}})
	if err != nil {
		return err
	}
	plan.fill(got)
	return nil
}

func (r *rdnsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan rdnsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error creating reverse DNS entry", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *rdnsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state rdnsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiRDNS](ctx, r.client, rdnsPath(state.IP.ValueString()), "rdns", nil)
	if client.NotFound(err) { // RDNS_NOT_FOUND, or IP_NOT_FOUND once the IP is gone
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading reverse DNS entry", err.Error())
		return
	}
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *rdnsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan rdnsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error updating reverse DNS entry", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *rdnsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state rdnsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodDelete, rdnsPath(state.IP.ValueString()), nil, nil)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error deleting reverse DNS entry", err.Error())
	}
}

func (r *rdnsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ip"), req, resp)
}

// --- data sources ---

func rdnsDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"ip":  dsStr("IP address."),
		"ptr": dsStr("PTR record."),
	}
}

// NewRDNSDataSource reads the reverse DNS entry of one IP address.
func NewRDNSDataSource() datasource.DataSource { return &rdnsDataSource{} }

type rdnsDataSource struct{ dataSourceBase }

func (d *rdnsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rdns"
}

func (d *rdnsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads the reverse DNS entry of one IP address.",
		Attributes: with(rdnsDSAttrs(), map[string]dschema.Attribute{
			"ip": dsRequiredStr("IP address."),
		}),
	}
}

func (d *rdnsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg rdnsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiRDNS](ctx, d.client, rdnsPath(cfg.IP.ValueString()), "rdns", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error reading reverse DNS entry", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewRDNSEntriesDataSource lists reverse DNS entries.
func NewRDNSEntriesDataSource() datasource.DataSource { return &rdnsEntriesDataSource{} }

type rdnsEntriesDataSource struct{ dataSourceBase }

type rdnsEntriesModel struct {
	ServerIP types.String `tfsdk:"server_ip"`
	Entries  []rdnsModel  `tfsdk:"entries"`
}

func (d *rdnsEntriesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rdns_entries"
}

func (d *rdnsEntriesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the reverse DNS entries of the account, or of one server.",
		Attributes: map[string]dschema.Attribute{
			"server_ip": dsOptionalStr("Main IP address of a server; only its entries are listed."),
			"entries":   dsList("The reverse DNS entries.", rdnsDSAttrs()),
		},
	}
}

func (d *rdnsEntriesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg rdnsEntriesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := url.Values{}
	formStr(query, "server_ip", cfg.ServerIP)
	list, err := client.GetList[apiRDNS](ctx, d.client, "/rdns", "rdns", query)
	if err != nil {
		resp.Diagnostics.AddError("Error listing reverse DNS entries", err.Error())
		return
	}
	cfg.Entries = make([]rdnsModel, len(list))
	for i, e := range list {
		cfg.Entries[i].fill(e)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
