package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

var (
	_ resource.Resource                = (*vswitchResource)(nil)
	_ resource.ResourceWithImportState = (*vswitchResource)(nil)
)

// vswitchTimeout bounds the wait for servers to join or leave a vSwitch.
const vswitchTimeout = 10 * time.Minute

// NewVSwitchResource manages a vSwitch and the servers attached to it.
func NewVSwitchResource() resource.Resource { return &vswitchResource{} }

type vswitchResource struct{ resourceBase }

// apiVSwitch is a vSwitch as the webservice answers it — a bare object, no
// envelope. The list GET /vswitch carries id, name, vlan and cancelled only.
type apiVSwitch struct {
	ID           int64                    `json:"id"`
	Name         string                   `json:"name"`
	VLAN         client.FlexInt           `json:"vlan"`
	Cancelled    bool                     `json:"cancelled"`
	Server       []apiVSwitchServer       `json:"server"`
	Subnet       []apiVSwitchSubnet       `json:"subnet"`
	CloudNetwork []apiVSwitchCloudNetwork `json:"cloud_network"`
}

type apiVSwitchServer struct {
	ServerIP      client.FlexString `json:"server_ip"`
	ServerIPv6Net client.FlexString `json:"server_ipv6_net"`
	ServerNumber  client.FlexInt    `json:"server_number"`
	Status        string            `json:"status"`
}

type apiVSwitchSubnet struct {
	IP      string            `json:"ip"`
	Mask    client.FlexInt    `json:"mask"`
	Gateway client.FlexString `json:"gateway"`
}

type apiVSwitchCloudNetwork struct {
	ID      client.FlexInt    `json:"id"`
	IP      string            `json:"ip"`
	Mask    client.FlexInt    `json:"mask"`
	Gateway client.FlexString `json:"gateway"`
}

var (
	vswitchServerType = types.ObjectType{AttrTypes: map[string]attr.Type{
		"server_number": types.Int64Type, "server_ip": types.StringType,
		"server_ipv6_net": types.StringType, "status": types.StringType,
	}}
	vswitchSubnetType = types.ObjectType{AttrTypes: map[string]attr.Type{
		"ip": types.StringType, "mask": types.Int64Type, "gateway": types.StringType,
	}}
	vswitchCloudNetworkType = types.ObjectType{AttrTypes: map[string]attr.Type{
		"id": types.Int64Type, "ip": types.StringType, "mask": types.Int64Type, "gateway": types.StringType,
	}}
)

type vswitchModel struct {
	ID               types.Int64  `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	VLAN             types.Int64  `tfsdk:"vlan"`
	Servers          types.Set    `tfsdk:"servers"`
	CancellationDate types.String `tfsdk:"cancellation_date"`
	Cancelled        types.Bool   `tfsdk:"cancelled"`
	ServerDetails    types.List   `tfsdk:"server_details"`
	Subnets          types.List   `tfsdk:"subnets"`
	CloudNetworks    types.List   `tfsdk:"cloud_networks"`
}

// vswitchDSModel is the data source's view: the resource without the
// destroy-time cancellation date.
type vswitchDSModel struct {
	ID            types.Int64  `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	VLAN          types.Int64  `tfsdk:"vlan"`
	Servers       types.Set    `tfsdk:"servers"`
	Cancelled     types.Bool   `tfsdk:"cancelled"`
	ServerDetails types.List   `tfsdk:"server_details"`
	Subnets       types.List   `tfsdk:"subnets"`
	CloudNetworks types.List   `tfsdk:"cloud_networks"`
}

func (m *vswitchModel) fill(v apiVSwitch) {
	var ds vswitchDSModel
	ds.fill(v)
	m.ID, m.Name, m.VLAN, m.Servers = ds.ID, ds.Name, ds.VLAN, ds.Servers
	m.Cancelled, m.ServerDetails, m.Subnets, m.CloudNetworks = ds.Cancelled, ds.ServerDetails, ds.Subnets, ds.CloudNetworks
}

func (m *vswitchDSModel) fill(v apiVSwitch) {
	m.ID = types.Int64Value(v.ID)
	m.Name = types.StringValue(v.Name)
	m.VLAN = flexInt(v.VLAN)
	m.Cancelled = types.BoolValue(v.Cancelled)

	numbers := make([]attr.Value, 0, len(v.Server))
	servers := make([]attr.Value, 0, len(v.Server))
	for _, s := range v.Server {
		numbers = append(numbers, flexInt(s.ServerNumber))
		servers = append(servers, types.ObjectValueMust(vswitchServerType.AttrTypes, map[string]attr.Value{
			"server_number": flexInt(s.ServerNumber), "server_ip": flexStr(s.ServerIP),
			"server_ipv6_net": flexStr(s.ServerIPv6Net), "status": types.StringValue(s.Status),
		}))
	}
	m.Servers = types.SetValueMust(types.Int64Type, numbers)
	m.ServerDetails = types.ListValueMust(vswitchServerType, servers)

	subnets := make([]attr.Value, 0, len(v.Subnet))
	for _, s := range v.Subnet {
		subnets = append(subnets, types.ObjectValueMust(vswitchSubnetType.AttrTypes, map[string]attr.Value{
			"ip": types.StringValue(s.IP), "mask": flexInt(s.Mask), "gateway": flexStr(s.Gateway),
		}))
	}
	m.Subnets = types.ListValueMust(vswitchSubnetType, subnets)

	clouds := make([]attr.Value, 0, len(v.CloudNetwork))
	for _, c := range v.CloudNetwork {
		clouds = append(clouds, types.ObjectValueMust(vswitchCloudNetworkType.AttrTypes, map[string]attr.Value{
			"id": flexInt(c.ID), "ip": types.StringValue(c.IP), "mask": flexInt(c.Mask), "gateway": flexStr(c.Gateway),
		}))
	}
	m.CloudNetworks = types.ListValueMust(vswitchCloudNetworkType, clouds)
}

func vswitchPath(id int64) string { return fmt.Sprintf("/vswitch/%d", id) }

func readVSwitch(ctx context.Context, c *client.Client, id int64) (apiVSwitch, error) {
	return client.GetObject[apiVSwitch](ctx, c, vswitchPath(id), "vswitch", nil)
}

// vswitchSend issues a write and repeats it while Robot is still applying the
// previous change (409 VSWITCH_IN_PROCESS).
func vswitchSend(ctx context.Context, c *client.Client, method, path string, form url.Values) error {
	return waitFor(ctx, vswitchTimeout, "the vSwitch to finish its previous change", func() (bool, error) {
		err := c.Send(ctx, method, path, form, nil)
		if client.HasCode(err, "VSWITCH_IN_PROCESS") {
			return false, nil
		}
		return true, err
	})
}

// vswitchWaitReady waits until no attached server is "in process"; a server
// that ended "failed" is an error.
func vswitchWaitReady(ctx context.Context, c *client.Client, id int64) (apiVSwitch, error) {
	var v apiVSwitch
	err := waitFor(ctx, vswitchTimeout, "the vSwitch servers to become ready", func() (bool, error) {
		var err error
		if v, err = readVSwitch(ctx, c, id); err != nil {
			return false, err
		}
		for _, s := range v.Server {
			switch s.Status {
			case "in process":
				return false, nil
			case "failed":
				return false, fmt.Errorf("server %d failed to join vSwitch %d", s.ServerNumber.Value, id)
			}
		}
		return true, nil
	})
	return v, err
}

// vswitchServerForm renders server[]=… for a list of server numbers.
func vswitchServerForm(numbers []int64) url.Values {
	form := url.Values{}
	for _, n := range numbers {
		form.Add("server[]", strconv.FormatInt(n, 10))
	}
	return form
}

func vswitchSetNumbers(ctx context.Context, s types.Set, diags *diag.Diagnostics) []int64 {
	if s.IsNull() || s.IsUnknown() {
		return nil
	}
	var out []int64
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func (r *vswitchResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vswitch"
}

func vswitchComputedAttrs() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"cancelled": computedBool("Whether the vSwitch is cancelled."),
		"server_details": schema.ListNestedAttribute{
			MarkdownDescription: "The attached servers with their vSwitch status (`ready`, `in process`, `failed`).",
			Computed:            true,
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"server_number":   computedInt("Server number."),
				"server_ip":       computedStr("Main IPv4 address of the server."),
				"server_ipv6_net": computedStr("Main IPv6 net of the server."),
				"status":          computedStr("Status of the server in this vSwitch."),
			}},
		},
		"subnets": schema.ListNestedAttribute{
			MarkdownDescription: "Subnets routed into the vSwitch.",
			Computed:            true,
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"ip":      computedStr("Network address."),
				"mask":    computedInt("Prefix length."),
				"gateway": computedStr("Gateway."),
			}},
		},
		"cloud_networks": schema.ListNestedAttribute{
			MarkdownDescription: "Hetzner Cloud networks coupled to the vSwitch.",
			Computed:            true,
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"id":      computedInt("Cloud network id."),
				"ip":      computedStr("Network address."),
				"mask":    computedInt("Prefix length."),
				"gateway": computedStr("Gateway."),
			}},
		},
	}
}

func (r *vswitchResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id":   stableInt("Numeric vSwitch id."),
		"name": reqStr("Name of the vSwitch."),
		"vlan": schema.Int64Attribute{
			MarkdownDescription: "VLAN id (4000–4091), unique among the account's vSwitches. Changes in place.",
			Required:            true,
			Validators:          []validator.Int64{int64validator.Between(4000, 4091)},
		},
		"servers": schema.SetAttribute{
			MarkdownDescription: "Numbers of the servers attached to the vSwitch. Left out, the attached servers are not managed; " +
				"`[]` detaches all of them. Joining takes a while — the apply waits until every server is `ready`.",
			Optional:      true,
			Computed:      true,
			ElementType:   types.Int64Type,
			PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
		},
		"cancellation_date": schema.StringAttribute{
			MarkdownDescription: "Used on destroy only: the date (`yyyy-MM-dd`) the vSwitch is cancelled to, or `now` (the default).",
			Optional:            true,
			Computed:            true,
			Default:             stringdefault.StaticString("now"),
			Validators: []validator.String{stringvalidator.RegexMatches(
				regexp.MustCompile(`^(now|\d{4}-\d{2}-\d{2})$`), "must be `now` or a date in the form yyyy-MM-dd")},
		},
	}
	for k, v := range vswitchComputedAttrs() {
		attrs[k] = v
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A vSwitch — a layer-2 network between dedicated servers (and optionally Hetzner Cloud networks). " +
			"Destroying it cancels the vSwitch, by default immediately; a cancelled vSwitch disappears from state.",
		Attributes: attrs,
	}
}

func (r *vswitchResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan vswitchModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	form := url.Values{"name": {plan.Name.ValueString()}, "vlan": {strconv.FormatInt(plan.VLAN.ValueInt64(), 10)}}
	created, err := client.SendObject[apiVSwitch](ctx, r.client, http.MethodPost, "/vswitch", "vswitch", form)
	if err != nil {
		resp.Diagnostics.AddError("Error creating vSwitch", err.Error())
		return
	}
	id := created.ID
	// Saved before the servers join, so a failure there leaves a tainted
	// vSwitch in state instead of an orphan.
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cancellation_date"), plan.CancellationDate)...)

	if numbers := vswitchSetNumbers(ctx, plan.Servers, &resp.Diagnostics); len(numbers) > 0 {
		if err := vswitchSend(ctx, r.client, http.MethodPost, vswitchPath(id)+"/server", vswitchServerForm(numbers)); err != nil {
			resp.Diagnostics.AddError("Error attaching servers to vSwitch", err.Error())
			return
		}
	}
	got, err := vswitchWaitReady(ctx, r.client, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading created vSwitch", err.Error())
		return
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vswitchResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state vswitchModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readVSwitch(ctx, r.client, state.ID.ValueInt64())
	if client.NotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading vSwitch", err.Error())
		return
	}
	if got.Cancelled {
		resp.State.RemoveResource(ctx)
		return
	}
	state.fill(got)
	if state.CancellationDate.IsNull() {
		state.CancellationDate = types.StringValue("now")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *vswitchResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state vswitchModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueInt64()
	if !plan.Name.Equal(state.Name) || !plan.VLAN.Equal(state.VLAN) {
		form := url.Values{"name": {plan.Name.ValueString()}, "vlan": {strconv.FormatInt(plan.VLAN.ValueInt64(), 10)}}
		if err := vswitchSend(ctx, r.client, http.MethodPost, vswitchPath(id), form); err != nil {
			resp.Diagnostics.AddError("Error updating vSwitch", err.Error())
			return
		}
	}

	if !plan.Servers.IsUnknown() && !plan.Servers.IsNull() {
		want := map[int64]bool{}
		for _, n := range vswitchSetNumbers(ctx, plan.Servers, &resp.Diagnostics) {
			want[n] = true
		}
		have := map[int64]bool{}
		for _, n := range vswitchSetNumbers(ctx, state.Servers, &resp.Diagnostics) {
			have[n] = true
		}
		var add, remove []int64
		for n := range want {
			if !have[n] {
				add = append(add, n)
			}
		}
		for n := range have {
			if !want[n] {
				remove = append(remove, n)
			}
		}
		sort.Slice(add, func(i, j int) bool { return add[i] < add[j] })
		sort.Slice(remove, func(i, j int) bool { return remove[i] < remove[j] })
		if len(remove) > 0 {
			if err := vswitchSend(ctx, r.client, http.MethodDelete, vswitchPath(id)+"/server", vswitchServerForm(remove)); err != nil {
				resp.Diagnostics.AddError("Error detaching servers from vSwitch", err.Error())
				return
			}
			if _, err := vswitchWaitReady(ctx, r.client, id); err != nil {
				resp.Diagnostics.AddError("Error detaching servers from vSwitch", err.Error())
				return
			}
		}
		if len(add) > 0 {
			if err := vswitchSend(ctx, r.client, http.MethodPost, vswitchPath(id)+"/server", vswitchServerForm(add)); err != nil {
				resp.Diagnostics.AddError("Error attaching servers to vSwitch", err.Error())
				return
			}
		}
	}

	got, err := vswitchWaitReady(ctx, r.client, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading updated vSwitch", err.Error())
		return
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *vswitchResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state vswitchModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	date := "now"
	if !state.CancellationDate.IsNull() && !state.CancellationDate.IsUnknown() {
		date = state.CancellationDate.ValueString()
	}
	err := vswitchSend(ctx, r.client, http.MethodDelete, vswitchPath(state.ID.ValueInt64()), url.Values{"cancellation_date": {date}})
	// 409 CONFLICT: already cancelled — gone either way.
	if err != nil && !client.NotFound(err) && !client.HasCode(err, "CONFLICT") {
		resp.Diagnostics.AddError("Error cancelling vSwitch", err.Error())
	}
}

func (r *vswitchResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := parseInt64(req.ID, "vSwitch id")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("cancellation_date"), "now")...)
}

// --- data sources ---

func vswitchDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"id":        dsInt("Numeric vSwitch id."),
		"name":      dsStr("Name of the vSwitch."),
		"vlan":      dsInt("VLAN id."),
		"cancelled": dsBool("Whether the vSwitch is cancelled."),
		"servers": dschema.SetAttribute{
			MarkdownDescription: "Numbers of the attached servers.",
			Computed:            true,
			ElementType:         types.Int64Type,
		},
		"server_details": dsList("The attached servers with their vSwitch status.", map[string]dschema.Attribute{
			"server_number":   dsInt("Server number."),
			"server_ip":       dsStr("Main IPv4 address of the server."),
			"server_ipv6_net": dsStr("Main IPv6 net of the server."),
			"status":          dsStr("Status of the server in this vSwitch."),
		}),
		"subnets": dsList("Subnets routed into the vSwitch.", map[string]dschema.Attribute{
			"ip":      dsStr("Network address."),
			"mask":    dsInt("Prefix length."),
			"gateway": dsStr("Gateway."),
		}),
		"cloud_networks": dsList("Hetzner Cloud networks coupled to the vSwitch.", map[string]dschema.Attribute{
			"id":      dsInt("Cloud network id."),
			"ip":      dsStr("Network address."),
			"mask":    dsInt("Prefix length."),
			"gateway": dsStr("Gateway."),
		}),
	}
}

// NewVSwitchDataSource reads one vSwitch.
func NewVSwitchDataSource() datasource.DataSource { return &vswitchDataSource{} }

type vswitchDataSource struct{ dataSourceBase }

func (d *vswitchDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vswitch"
}

func (d *vswitchDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads one vSwitch with its servers, subnets and cloud networks.",
		Attributes: with(vswitchDSAttrs(), map[string]dschema.Attribute{
			"id": dsRequiredInt("Numeric vSwitch id."),
		}),
	}
}

func (d *vswitchDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg vswitchDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readVSwitch(ctx, d.client, cfg.ID.ValueInt64())
	if err != nil {
		resp.Diagnostics.AddError("Error reading vSwitch", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewVSwitchesDataSource lists the account's vSwitches.
func NewVSwitchesDataSource() datasource.DataSource { return &vswitchesDataSource{} }

type vswitchesDataSource struct{ dataSourceBase }

type vswitchSummaryModel struct {
	ID        types.Int64  `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	VLAN      types.Int64  `tfsdk:"vlan"`
	Cancelled types.Bool   `tfsdk:"cancelled"`
}

type vswitchesModel struct {
	VSwitches []vswitchSummaryModel `tfsdk:"vswitches"`
}

func (d *vswitchesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vswitches"
}

func (d *vswitchesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the account's vSwitches, cancelled ones included. Read a single one with " +
			"`hetznerrobot_vswitch` for its servers and subnets.",
		Attributes: map[string]dschema.Attribute{
			"vswitches": dsList("The vSwitches.", map[string]dschema.Attribute{
				"id":        dsInt("Numeric vSwitch id."),
				"name":      dsStr("Name of the vSwitch."),
				"vlan":      dsInt("VLAN id."),
				"cancelled": dsBool("Whether the vSwitch is cancelled."),
			}),
		},
	}
}

func (d *vswitchesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	var list []apiVSwitch
	err := d.client.Get(ctx, "/vswitch", nil, &list)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error listing vSwitches", err.Error())
		return
	}
	out := vswitchesModel{VSwitches: make([]vswitchSummaryModel, len(list))}
	for i, v := range list {
		out.VSwitches[i] = vswitchSummaryModel{
			ID: types.Int64Value(v.ID), Name: types.StringValue(v.Name),
			VLAN: flexInt(v.VLAN), Cancelled: types.BoolValue(v.Cancelled),
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}
