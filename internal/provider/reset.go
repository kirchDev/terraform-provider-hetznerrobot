package provider

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

// Resets and Wake on LAN are actions, not state. Their resources run the
// action on create — and again whenever `triggers` change, which replaces them
// — and do nothing on read or destroy, like terraform_data.

var (
	_ resource.Resource                = (*serverResetResource)(nil)
	_ resource.ResourceWithImportState = (*serverResetResource)(nil)
)

// NewServerResetResource resets a server.
func NewServerResetResource() resource.Resource { return &serverResetResource{} }

type serverResetResource struct{ resourceBase }

type serverResetModel struct {
	ServerNumber types.Int64  `tfsdk:"server_number"`
	Type         types.String `tfsdk:"type"`
	Triggers     types.Map    `tfsdk:"triggers"`
}

func resetPath(serverNumber int64) string { return "/reset/" + strconv.FormatInt(serverNumber, 10) }

func (r *serverResetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_reset"
}

func (r *serverResetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	typ := reqStrNew("Reset to execute: `sw` (CTRL+ALT+DEL), `hw` (hardware reset), `man` (manual reset by a technician), " +
		"`power` (press the power button) or `power_long` (hold it; forces a power-off). " +
		"`hetznerrobot_reset.types` lists what the server supports.")
	typ.Validators = []validator.String{stringvalidator.OneOf("sw", "hw", "man", "power", "power_long")}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Resets a dedicated server — once on creation, and again whenever `triggers` change. " +
			"Typical use: reboot into a freshly activated `hetznerrobot_boot_rescue` by putting its `password` into `triggers`. " +
			"Reading and destroying the resource do nothing.",
		Attributes: map[string]schema.Attribute{
			"server_number": reqIntNew("Server to reset."),
			"type":          typ,
			"triggers":      triggersAttr(),
		},
	}
}

func (r *serverResetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverResetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodPost, resetPath(plan.ServerNumber.ValueInt64()), url.Values{"type": {plan.Type.ValueString()}}, nil)
	if err != nil {
		resp.Diagnostics.AddError("Error resetting server", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverResetResource) Read(context.Context, resource.ReadRequest, *resource.ReadResponse) {}

func (r *serverResetResource) Update(_ context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.State.Raw = req.Plan.Raw
}

func (r *serverResetResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

// ImportState takes "<server_number>/<type>" and records it without resetting
// anything.
func (r *serverResetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, "server_number", "type")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	n, err := parseInt64(parts[0], "server_number")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_number"), n)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("type"), parts[1])...)
}

var (
	_ resource.Resource                = (*wakeOnLANResource)(nil)
	_ resource.ResourceWithImportState = (*wakeOnLANResource)(nil)
)

// NewWakeOnLANResource sends a Wake on LAN packet to a server.
func NewWakeOnLANResource() resource.Resource { return &wakeOnLANResource{} }

type wakeOnLANResource struct{ resourceBase }

type wakeOnLANModel struct {
	ServerNumber types.Int64 `tfsdk:"server_number"`
	Triggers     types.Map   `tfsdk:"triggers"`
}

func (r *wakeOnLANResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_wake_on_lan"
}

func (r *wakeOnLANResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sends a Wake on LAN packet to a dedicated server — once on creation, and again whenever " +
			"`triggers` change. The server must support it (`hetznerrobot_server.wol`). Reading and destroying the resource do nothing.",
		Attributes: map[string]schema.Attribute{
			"server_number": reqIntNew("Server to wake."),
			"triggers":      triggersAttr(),
		},
	}
}

func (r *wakeOnLANResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan wakeOnLANModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodPost, "/wol/"+strconv.FormatInt(plan.ServerNumber.ValueInt64(), 10), url.Values{}, nil)
	if err != nil {
		resp.Diagnostics.AddError("Error sending Wake on LAN packet", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *wakeOnLANResource) Read(context.Context, resource.ReadRequest, *resource.ReadResponse) {}

func (r *wakeOnLANResource) Update(_ context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.State.Raw = req.Plan.Raw
}

func (r *wakeOnLANResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

// ImportState takes the server number and records it without sending anything.
func (r *wakeOnLANResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := parseInt64(req.ID, "server_number")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_number"), n)...)
}

// --- data sources ---

type apiReset struct {
	ServerIP        string              `json:"server_ip"`
	ServerIPv6Net   string              `json:"server_ipv6_net"`
	ServerNumber    client.FlexInt      `json:"server_number"`
	Type            client.StringOrList `json:"type"`
	OperatingStatus client.FlexString   `json:"operating_status"`
}

type resetModel struct {
	ServerNumber    types.Int64  `tfsdk:"server_number"`
	ServerIP        types.String `tfsdk:"server_ip"`
	ServerIPv6Net   types.String `tfsdk:"server_ipv6_net"`
	Types           types.List   `tfsdk:"types"`
	OperatingStatus types.String `tfsdk:"operating_status"`
}

type resetsEntryModel struct {
	ServerNumber  types.Int64  `tfsdk:"server_number"`
	ServerIP      types.String `tfsdk:"server_ip"`
	ServerIPv6Net types.String `tfsdk:"server_ipv6_net"`
	Types         types.List   `tfsdk:"types"`
}

func resetsEntryDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"server_number":   dsInt("Server number."),
		"server_ip":       dsStr("Main IP address of the server."),
		"server_ipv6_net": dsStr("Main IPv6 net of the server."),
		"types":           dsStrList("Reset types the server supports (`sw`, `hw`, `man`, `power`, `power_long`)."),
	}
}

// NewResetDataSource reads the reset options of a server.
func NewResetDataSource() datasource.DataSource { return &resetDataSource{} }

type resetDataSource struct{ dataSourceBase }

func (d *resetDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_reset"
}

func (d *resetDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads the reset options of a dedicated server and, where Robot can tell, its operating status.",
		Attributes: with(resetsEntryDSAttrs(), map[string]dschema.Attribute{
			"server_number":    dsRequiredInt("Server to read."),
			"operating_status": dsStr("Operating status, e.g. `running`, or `not supported`."),
		}),
	}
}

func (d *resetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg resetModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiReset](ctx, d.client, resetPath(cfg.ServerNumber.ValueInt64()), "reset", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error reading reset options", err.Error())
		return
	}
	cfg.ServerIP = types.StringValue(got.ServerIP)
	cfg.ServerIPv6Net = types.StringValue(got.ServerIPv6Net)
	cfg.Types = stringList(got.Type.Values())
	cfg.OperatingStatus = flexStr(got.OperatingStatus)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewResetsDataSource lists the reset options of every server.
func NewResetsDataSource() datasource.DataSource { return &resetsDataSource{} }

type resetsDataSource struct{ dataSourceBase }

type resetsModel struct {
	Servers []resetsEntryModel `tfsdk:"servers"`
}

func (d *resetsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_resets"
}

func (d *resetsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the reset options of every server that has any.",
		Attributes: map[string]dschema.Attribute{
			"servers": dsList("The servers and their reset options.", resetsEntryDSAttrs()),
		},
	}
}

func (d *resetsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := client.GetList[apiReset](ctx, d.client, "/reset", "reset", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error listing reset options", err.Error())
		return
	}
	out := resetsModel{Servers: make([]resetsEntryModel, len(list))}
	for i, x := range list {
		out.Servers[i] = resetsEntryModel{
			ServerNumber: flexInt(x.ServerNumber), ServerIP: types.StringValue(x.ServerIP),
			ServerIPv6Net: types.StringValue(x.ServerIPv6Net), Types: stringList(x.Type.Values()),
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}
