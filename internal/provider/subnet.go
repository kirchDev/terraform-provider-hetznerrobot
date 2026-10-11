package provider

import (
	"context"
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

// ===================== subnet =====================

var (
	_ resource.Resource                = (*subnetResource)(nil)
	_ resource.ResourceWithImportState = (*subnetResource)(nil)
)

// NewSubnetResource adopts a subnet and manages its traffic warnings.
func NewSubnetResource() resource.Resource { return &subnetResource{} }

type subnetResource struct{ resourceBase }

type apiSubnet struct {
	apiTraffic
	IP           string            `json:"ip"`
	Mask         client.FlexInt    `json:"mask"`
	Gateway      client.FlexString `json:"gateway"`
	ServerIP     client.FlexString `json:"server_ip"`
	ServerNumber client.FlexInt    `json:"server_number"`
	Failover     bool              `json:"failover"`
	Locked       bool              `json:"locked"`
}

type subnetModel struct {
	trafficSettings
	IP           types.String `tfsdk:"ip"`
	Mask         types.Int64  `tfsdk:"mask"`
	Gateway      types.String `tfsdk:"gateway"`
	ServerIP     types.String `tfsdk:"server_ip"`
	ServerNumber types.Int64  `tfsdk:"server_number"`
	Failover     types.Bool   `tfsdk:"failover"`
	Locked       types.Bool   `tfsdk:"locked"`
}

func (m *subnetModel) fill(a apiSubnet) {
	m.trafficSettings.fill(a.apiTraffic)
	m.IP = types.StringValue(a.IP)
	m.Mask = flexInt(a.Mask)
	m.Gateway = flexStr(a.Gateway)
	m.ServerIP = flexStr(a.ServerIP)
	m.ServerNumber = flexInt(a.ServerNumber)
	m.Failover = types.BoolValue(a.Failover)
	m.Locked = types.BoolValue(a.Locked)
}

func subnetPath(ip string) string { return "/subnet/" + escape(ip) }

func readSubnet(ctx context.Context, c *client.Client, ip string) (apiSubnet, error) {
	return client.GetObject[apiSubnet](ctx, c, subnetPath(ip), "subnet", nil)
}

func (r *subnetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnet"
}

func (r *subnetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An IPv4 or IPv6 subnet of a server, adopted to manage its traffic warnings. Subnets cannot be " +
			"created here (order one with `hetznerrobot_server_addon_order`). Destroying the resource only forgets the subnet; " +
			"cancel it with `hetznerrobot_subnet_cancellation`.",
		Attributes: mergeAttrs(trafficAttrs(), map[string]schema.Attribute{
			"ip":            reqStrNew("Net address of the subnet, e.g. `2a01:4f8:111:4221::`."),
			"mask":          stableInt("Prefix length."),
			"gateway":       stableStr("Gateway of the subnet."),
			"server_ip":     computedStr("Main IP address of the server the subnet is routed to."),
			"server_number": computedInt("Server the subnet belongs to."),
			"failover":      computedBool("Whether the subnet is a failover subnet."),
			"locked":        computedBool("Whether the subnet is locked."),
		}),
	}
}

func (r *subnetResource) apply(ctx context.Context, plan *subnetModel) error {
	if plan.configured() {
		if _, err := client.SendObject[apiSubnet](ctx, r.client, http.MethodPost, subnetPath(plan.IP.ValueString()), "subnet",
			plan.form()); err != nil {
			return err
		}
	}
	got, err := readSubnet(ctx, r.client, plan.IP.ValueString())
	if err != nil {
		return err
	}
	plan.fill(got)
	return nil
}

func (r *subnetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan subnetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error updating subnet traffic warnings", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *subnetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state subnetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readSubnet(ctx, r.client, state.IP.ValueString())
	if client.NotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading subnet", err.Error())
		return
	}
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *subnetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan subnetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error updating subnet traffic warnings", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete only forgets the subnet; cancelling it is hetznerrobot_subnet_cancellation.
func (r *subnetResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

func (r *subnetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ip"), req, resp)
}

// ===================== separate MAC of a subnet =====================

var (
	_ resource.Resource                = (*subnetMACResource)(nil)
	_ resource.ResourceWithImportState = (*subnetMACResource)(nil)
)

// NewSubnetMACResource sets the MAC address an IPv6 subnet is routed to.
func NewSubnetMACResource() resource.Resource { return &subnetMACResource{} }

type subnetMACResource struct{ resourceBase }

type apiSubnetMAC struct {
	IP          string            `json:"ip"`
	Mask        client.FlexString `json:"mask"`
	MAC         client.FlexString `json:"mac"`
	PossibleMAC map[string]string `json:"possible_mac"`
}

type subnetMACModel struct {
	IP          types.String `tfsdk:"ip"`
	MAC         types.String `tfsdk:"mac"`
	Mask        types.String `tfsdk:"mask"`
	PossibleMAC types.Map    `tfsdk:"possible_mac"`
}

func (m *subnetMACModel) fill(a apiSubnetMAC) {
	m.MAC = flexStr(a.MAC)
	m.Mask = flexStr(a.Mask)
	elems := make(map[string]attr.Value, len(a.PossibleMAC))
	for ip, mac := range a.PossibleMAC {
		elems[ip] = types.StringValue(mac)
	}
	m.PossibleMAC = types.MapValueMust(types.StringType, elems)
}

func subnetMACPath(ip string) string { return subnetPath(ip) + "/mac" }

func (r *subnetMACResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnet_mac"
}

func (r *subnetMACResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The MAC address an IPv6 subnet is routed to — one of the server's `possible_mac` " +
			"addresses (see the `hetznerrobot_subnet_mac` data source). Destroying the resource resets it to the MAC of " +
			"the server's main IP. IPv4 subnets do not support this (`MAC_NOT_AVAILABLE`). Limited to 10 changes per hour.",
		Attributes: map[string]schema.Attribute{
			"ip":   reqStrNew("Net address of the subnet."),
			"mac":  reqStr("Target MAC address."),
			"mask": stableStr("Prefix length of the subnet."),
			"possible_mac": schema.MapAttribute{
				MarkdownDescription: "MAC addresses the subnet can be routed to, keyed by IP address.",
				Computed:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (r *subnetMACResource) write(ctx context.Context, plan *subnetMACModel) error {
	got, err := client.SendObject[apiSubnetMAC](ctx, r.client, http.MethodPut, subnetMACPath(plan.IP.ValueString()), "mac",
		url.Values{"mac": {plan.MAC.ValueString()}})
	if err != nil {
		return err
	}
	plan.fill(got)
	return nil
}

func (r *subnetMACResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan subnetMACModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error setting subnet MAC address", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *subnetMACResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state subnetMACModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiSubnetMAC](ctx, r.client, subnetMACPath(state.IP.ValueString()), "mac", nil)
	if client.NotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading subnet MAC address", err.Error())
		return
	}
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *subnetMACResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan subnetMACModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error setting subnet MAC address", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *subnetMACResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state subnetMACModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodDelete, subnetMACPath(state.IP.ValueString()), nil, nil)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error resetting subnet MAC address", err.Error())
	}
}

func (r *subnetMACResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ip"), req, resp)
}

// ===================== data sources =====================

func subnetDSAttrs() map[string]dschema.Attribute {
	return mergeAttrs(trafficDSAttrs(), map[string]dschema.Attribute{
		"ip":            dsStr("Net address of the subnet."),
		"mask":          dsInt("Prefix length."),
		"gateway":       dsStr("Gateway of the subnet."),
		"server_ip":     dsStr("Main IP address of the server the subnet is routed to."),
		"server_number": dsInt("Server the subnet belongs to."),
		"failover":      dsBool("Whether the subnet is a failover subnet."),
		"locked":        dsBool("Whether the subnet is locked."),
	})
}

// NewSubnetDataSource reads one subnet.
func NewSubnetDataSource() datasource.DataSource { return &subnetDataSource{} }

type subnetDataSource struct{ dataSourceBase }

func (d *subnetDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnet"
}

func (d *subnetDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads one subnet of the account.",
		Attributes: with(subnetDSAttrs(), map[string]dschema.Attribute{
			"ip": dsRequiredStr("Net address of the subnet."),
		}),
	}
}

func (d *subnetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg subnetModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readSubnet(ctx, d.client, cfg.IP.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading subnet", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewSubnetsDataSource lists the subnets of the account or a server.
func NewSubnetsDataSource() datasource.DataSource { return &subnetsDataSource{} }

type subnetsDataSource struct{ dataSourceBase }

type subnetsModel struct {
	ServerIP types.String  `tfsdk:"server_ip"`
	Subnets  []subnetModel `tfsdk:"subnets"`
}

func (d *subnetsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnets"
}

func (d *subnetsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the subnets of the account, or of one server.",
		Attributes: map[string]dschema.Attribute{
			"server_ip": dsOptionalStr("Main IP address of a server; only its subnets are listed."),
			"subnets":   dsList("The subnets.", subnetDSAttrs()),
		},
	}
}

func (d *subnetsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg subnetsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := url.Values{}
	formStr(query, "server_ip", cfg.ServerIP)
	list, err := client.GetList[apiSubnet](ctx, d.client, "/subnet", "subnet", query)
	if err != nil {
		resp.Diagnostics.AddError("Error listing subnets", err.Error())
		return
	}
	cfg.Subnets = make([]subnetModel, len(list))
	for i, a := range list {
		cfg.Subnets[i].fill(a)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewSubnetMACDataSource reads the MAC routing of an IPv6 subnet.
func NewSubnetMACDataSource() datasource.DataSource { return &subnetMACDataSource{} }

type subnetMACDataSource struct{ dataSourceBase }

func (d *subnetMACDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_subnet_mac"
}

func (d *subnetMACDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads the MAC address an IPv6 subnet is routed to, and the ones it could be routed to.",
		Attributes: map[string]dschema.Attribute{
			"ip":           dsRequiredStr("Net address of the subnet."),
			"mac":          dsStr("Current MAC address."),
			"mask":         dsStr("Prefix length of the subnet."),
			"possible_mac": dsStrMap("MAC addresses the subnet can be routed to, keyed by IP address."),
		},
	}
}

func (d *subnetMACDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg subnetMACModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiSubnetMAC](ctx, d.client, subnetMACPath(cfg.IP.ValueString()), "mac", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error reading subnet MAC address", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
