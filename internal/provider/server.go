package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

var (
	_ resource.Resource                = (*serverResource)(nil)
	_ resource.ResourceWithImportState = (*serverResource)(nil)
)

// NewServerResource adopts an existing dedicated server and manages its name.
func NewServerResource() resource.Resource { return &serverResource{} }

type serverResource struct{ resourceBase }

type apiServerSubnet struct {
	IP   string            `json:"ip"`
	Mask client.FlexString `json:"mask"`
}

// apiServer is GET /server/{n}. The list (GET /server) carries everything up
// to subnet but none of the availability flags.
type apiServer struct {
	ServerIP         client.FlexString `json:"server_ip"`
	ServerIPv6Net    client.FlexString `json:"server_ipv6_net"`
	ServerNumber     int64             `json:"server_number"`
	ServerName       string            `json:"server_name"`
	Product          string            `json:"product"`
	DC               string            `json:"dc"`
	Traffic          string            `json:"traffic"`
	Status           string            `json:"status"`
	Cancelled        bool              `json:"cancelled"`
	PaidUntil        client.FlexString `json:"paid_until"`
	IP               []string          `json:"ip"`
	Subnet           []apiServerSubnet `json:"subnet"`
	Reset            *bool             `json:"reset"`
	Rescue           *bool             `json:"rescue"`
	VNC              *bool             `json:"vnc"`
	Windows          *bool             `json:"windows"`
	Plesk            *bool             `json:"plesk"`
	CPanel           *bool             `json:"cpanel"`
	WOL              *bool             `json:"wol"`
	HotSwap          *bool             `json:"hot_swap"`
	LinkedStorageBox client.FlexInt    `json:"linked_storagebox"`
}

var serverSubnetType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"ip":   types.StringType,
	"mask": types.StringType,
}}

func serverSubnets(in []apiServerSubnet) types.List {
	elems := make([]attr.Value, len(in))
	for i, s := range in {
		elems[i] = types.ObjectValueMust(serverSubnetType.AttrTypes, map[string]attr.Value{
			"ip":   types.StringValue(s.IP),
			"mask": types.StringValue(s.Mask.Value),
		})
	}
	return types.ListValueMust(serverSubnetType, elems)
}

func boolPtr(b *bool) types.Bool {
	if b == nil {
		return types.BoolNull()
	}
	return types.BoolValue(*b)
}

// serverBaseModel holds what both the single read and the list deliver.
type serverBaseModel struct {
	ServerNumber  types.Int64  `tfsdk:"server_number"`
	ServerName    types.String `tfsdk:"server_name"`
	ServerIP      types.String `tfsdk:"server_ip"`
	ServerIPv6Net types.String `tfsdk:"server_ipv6_net"`
	Product       types.String `tfsdk:"product"`
	DC            types.String `tfsdk:"dc"`
	Traffic       types.String `tfsdk:"traffic"`
	Status        types.String `tfsdk:"status"`
	Cancelled     types.Bool   `tfsdk:"cancelled"`
	PaidUntil     types.String `tfsdk:"paid_until"`
	IPs           types.List   `tfsdk:"ips"`
	Subnets       types.List   `tfsdk:"subnets"`
}

func (m *serverBaseModel) fill(s apiServer) {
	m.ServerNumber = types.Int64Value(s.ServerNumber)
	m.ServerName = types.StringValue(s.ServerName)
	m.ServerIP = flexStr(s.ServerIP)
	m.ServerIPv6Net = flexStr(s.ServerIPv6Net)
	m.Product = types.StringValue(s.Product)
	m.DC = types.StringValue(s.DC)
	m.Traffic = types.StringValue(s.Traffic)
	m.Status = types.StringValue(s.Status)
	m.Cancelled = types.BoolValue(s.Cancelled)
	m.PaidUntil = flexStr(s.PaidUntil)
	if s.IP == nil {
		s.IP = []string{}
	}
	m.IPs = stringList(s.IP)
	m.Subnets = serverSubnets(s.Subnet)
}

type serverModel struct {
	serverBaseModel
	Reset            types.Bool  `tfsdk:"reset"`
	Rescue           types.Bool  `tfsdk:"rescue"`
	VNC              types.Bool  `tfsdk:"vnc"`
	Windows          types.Bool  `tfsdk:"windows"`
	Plesk            types.Bool  `tfsdk:"plesk"`
	CPanel           types.Bool  `tfsdk:"cpanel"`
	WOL              types.Bool  `tfsdk:"wol"`
	HotSwap          types.Bool  `tfsdk:"hot_swap"`
	LinkedStorageBox types.Int64 `tfsdk:"linked_storagebox"`
}

func (m *serverModel) fill(s apiServer) {
	m.serverBaseModel.fill(s)
	m.Reset = boolPtr(s.Reset)
	m.Rescue = boolPtr(s.Rescue)
	m.VNC = boolPtr(s.VNC)
	m.Windows = boolPtr(s.Windows)
	m.Plesk = boolPtr(s.Plesk)
	m.CPanel = boolPtr(s.CPanel)
	m.WOL = boolPtr(s.WOL)
	m.HotSwap = boolPtr(s.HotSwap)
	m.LinkedStorageBox = flexInt(s.LinkedStorageBox)
}

func serverPath(n int64) string { return fmt.Sprintf("/server/%d", n) }

func readServer(ctx context.Context, c *client.Client, n int64) (apiServer, error) {
	return client.GetObject[apiServer](ctx, c, serverPath(n), "server", nil)
}

func (r *serverResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (r *serverResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An existing dedicated server, adopted by its number — the webservice cannot create one here " +
			"(order it with `hetznerrobot_server_order` or `hetznerrobot_server_market_order`). Only the name is managed. " +
			"Destroying the resource only forgets the server; it keeps running and stays billed. " +
			"Cancel it with `hetznerrobot_server_cancellation`.",
		Attributes: map[string]schema.Attribute{
			"server_number":   reqIntNew("Server number (the server's id in Robot)."),
			"server_name":     optStr("Name of the server as shown in Robot."),
			"server_ip":       stableStr("Main IPv4 address; null on an IPv6-only server."),
			"server_ipv6_net": stableStr("Main IPv6 net address."),
			"product":         stableStr("Product name, e.g. `EX44`."),
			"dc":              stableStr("Data center, e.g. `FSN1-DC14`."),
			"traffic":         computedStr("Free traffic quota; `unlimited` for unlimited traffic."),
			"status":          computedStr("`ready` or `in process`."),
			"cancelled":       computedBool("Whether the server is cancelled."),
			"paid_until":      computedStr("Paid-until date (`yyyy-MM-dd`)."),
			"ips":             computedStrList("Single IP addresses assigned to the server."),
			"subnets": schema.ListNestedAttribute{
				MarkdownDescription: "Subnets assigned to the server.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"ip":   computedStr("Net address."),
					"mask": computedStr("Prefix length, e.g. `64`."),
				}},
			},
			"reset":             computedBool("Whether the reset system is available."),
			"rescue":            computedBool("Whether the Rescue System is available."),
			"vnc":               computedBool("Whether the VNC installation is available."),
			"windows":           computedBool("Whether the Windows installation is available."),
			"plesk":             computedBool("Whether the Plesk installation is available."),
			"cpanel":            computedBool("Whether the cPanel installation is available."),
			"wol":               computedBool("Whether Wake on LAN is available."),
			"hot_swap":          computedBool("Whether hot swap is available."),
			"linked_storagebox": computedInt("Id of the linked Storage Box, if any."),
		},
	}
}

func (r *serverResource) rename(ctx context.Context, n int64, name string) (apiServer, error) {
	return client.SendObject[apiServer](ctx, r.client, http.MethodPost, serverPath(n), "server",
		url.Values{"server_name": {name}})
}

func (r *serverResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	n := plan.ServerNumber.ValueInt64()
	got, err := readServer(ctx, r.client, n)
	if client.NotFound(err) {
		resp.Diagnostics.AddAttributeError(path.Root("server_number"), "Server not found",
			fmt.Sprintf("The account has no server %d. This resource adopts an existing server; it cannot create one.", n))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading server", err.Error())
		return
	}
	if !plan.ServerName.IsUnknown() && !plan.ServerName.IsNull() && plan.ServerName.ValueString() != got.ServerName {
		if _, err := r.rename(ctx, n, plan.ServerName.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error renaming server", err.Error())
			return
		}
		if got, err = readServer(ctx, r.client, n); err != nil {
			resp.Diagnostics.AddError("Error reading server", err.Error())
			return
		}
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readServer(ctx, r.client, state.ServerNumber.ValueInt64())
	if client.NotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading server", err.Error())
		return
	}
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serverResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	n := plan.ServerNumber.ValueInt64()
	if !plan.ServerName.IsUnknown() && !plan.ServerName.IsNull() {
		if _, err := r.rename(ctx, n, plan.ServerName.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error renaming server", err.Error())
			return
		}
	}
	got, err := readServer(ctx, r.client, n)
	if err != nil {
		resp.Diagnostics.AddError("Error reading server", err.Error())
		return
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete only forgets the server: the webservice cannot delete one, and a
// cancellation is a decision of its own (hetznerrobot_server_cancellation).
func (r *serverResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

func (r *serverResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := parseInt64(req.ID, "server_number")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_number"), n)...)
}

// --- data sources ---

func serverBaseDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"server_number":   dsInt("Server number."),
		"server_name":     dsStr("Name of the server."),
		"server_ip":       dsStr("Main IPv4 address; null on an IPv6-only server."),
		"server_ipv6_net": dsStr("Main IPv6 net address."),
		"product":         dsStr("Product name."),
		"dc":              dsStr("Data center."),
		"traffic":         dsStr("Free traffic quota; `unlimited` for unlimited traffic."),
		"status":          dsStr("`ready` or `in process`."),
		"cancelled":       dsBool("Whether the server is cancelled."),
		"paid_until":      dsStr("Paid-until date."),
		"ips":             dsStrList("Single IP addresses assigned to the server."),
		"subnets": dsList("Subnets assigned to the server.", map[string]dschema.Attribute{
			"ip":   dsStr("Net address."),
			"mask": dsStr("Prefix length."),
		}),
	}
}

// NewServerDataSource reads one server.
func NewServerDataSource() datasource.DataSource { return &serverDataSource{} }

type serverDataSource struct{ dataSourceBase }

func (d *serverDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (d *serverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads one dedicated server, including which features (reset, Rescue System, installations, " +
			"Wake on LAN) it supports.",
		Attributes: with(serverBaseDSAttrs(), map[string]dschema.Attribute{
			"server_number":     dsRequiredInt("Server number."),
			"reset":             dsBool("Whether the reset system is available."),
			"rescue":            dsBool("Whether the Rescue System is available."),
			"vnc":               dsBool("Whether the VNC installation is available."),
			"windows":           dsBool("Whether the Windows installation is available."),
			"plesk":             dsBool("Whether the Plesk installation is available."),
			"cpanel":            dsBool("Whether the cPanel installation is available."),
			"wol":               dsBool("Whether Wake on LAN is available."),
			"hot_swap":          dsBool("Whether hot swap is available."),
			"linked_storagebox": dsInt("Id of the linked Storage Box, if any."),
		}),
	}
}

func (d *serverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg serverModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readServer(ctx, d.client, cfg.ServerNumber.ValueInt64())
	if err != nil {
		resp.Diagnostics.AddError("Error reading server", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewServersDataSource lists every server of the account.
func NewServersDataSource() datasource.DataSource { return &serversDataSource{} }

type serversDataSource struct{ dataSourceBase }

type serversModel struct {
	Servers []serverBaseModel `tfsdk:"servers"`
}

func (d *serversDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_servers"
}

func (d *serversDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists every dedicated server of the account. The listing carries no feature flags — " +
			"read `hetznerrobot_server` for those.",
		Attributes: map[string]dschema.Attribute{
			"servers": dsList("The servers.", serverBaseDSAttrs()),
		},
	}
}

func (d *serversDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := client.GetList[apiServer](ctx, d.client, "/server", "server", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error listing servers", err.Error())
		return
	}
	out := serversModel{Servers: make([]serverBaseModel, len(list))}
	for i, s := range list {
		out.Servers[i].fill(s)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}
