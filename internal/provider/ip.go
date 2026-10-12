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

// --- traffic warnings, shared by single IPs and subnets ---

// trafficSettings are the four writable fields of an IP and of a subnet.
type trafficSettings struct {
	TrafficWarnings types.Bool  `tfsdk:"traffic_warnings"`
	TrafficHourly   types.Int64 `tfsdk:"traffic_hourly"`
	TrafficDaily    types.Int64 `tfsdk:"traffic_daily"`
	TrafficMonthly  types.Int64 `tfsdk:"traffic_monthly"`
}

type apiTraffic struct {
	TrafficWarnings bool           `json:"traffic_warnings"`
	TrafficHourly   client.FlexInt `json:"traffic_hourly"`
	TrafficDaily    client.FlexInt `json:"traffic_daily"`
	TrafficMonthly  client.FlexInt `json:"traffic_monthly"`
}

func (t *trafficSettings) fill(a apiTraffic) {
	t.TrafficWarnings = types.BoolValue(a.TrafficWarnings)
	t.TrafficHourly = flexInt(a.TrafficHourly)
	t.TrafficDaily = flexInt(a.TrafficDaily)
	t.TrafficMonthly = flexInt(a.TrafficMonthly)
}

// configured reports whether the plan holds any known traffic value.
func (t trafficSettings) configured() bool {
	for _, k := range []bool{
		t.TrafficWarnings.IsNull() || t.TrafficWarnings.IsUnknown(),
		t.TrafficHourly.IsNull() || t.TrafficHourly.IsUnknown(),
		t.TrafficDaily.IsNull() || t.TrafficDaily.IsUnknown(),
		t.TrafficMonthly.IsNull() || t.TrafficMonthly.IsUnknown(),
	} {
		if !k {
			return true
		}
	}
	return false
}

// form sends every known value, not only the changed one: the four belong
// together, and the documentation does not promise that one alone is accepted.
func (t trafficSettings) form() url.Values {
	f := url.Values{}
	formBool(f, "traffic_warnings", t.TrafficWarnings)
	formInt(f, "traffic_hourly", t.TrafficHourly)
	formInt(f, "traffic_daily", t.TrafficDaily)
	formInt(f, "traffic_monthly", t.TrafficMonthly)
	return f
}

func trafficAttrs() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"traffic_warnings": optBool("Whether Robot sends traffic warnings."),
		"traffic_hourly":   optInt("Hourly traffic limit in MB that triggers a warning."),
		"traffic_daily":    optInt("Daily traffic limit in MB that triggers a warning."),
		"traffic_monthly":  optInt("Monthly traffic limit in GB that triggers a warning."),
	}
}

func trafficDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"traffic_warnings": dsBool("Whether Robot sends traffic warnings."),
		"traffic_hourly":   dsInt("Hourly traffic limit in MB."),
		"traffic_daily":    dsInt("Daily traffic limit in MB."),
		"traffic_monthly":  dsInt("Monthly traffic limit in GB."),
	}
}

func mergeAttrs[T any](maps ...map[string]T) map[string]T {
	out := map[string]T{}
	for _, m := range maps {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// ===================== single IP =====================

var (
	_ resource.Resource                = (*ipResource)(nil)
	_ resource.ResourceWithImportState = (*ipResource)(nil)
)

// NewIPResource adopts a single IP address and manages its traffic warnings.
func NewIPResource() resource.Resource { return &ipResource{} }

type ipResource struct{ resourceBase }

// apiIP is GET /ip/{ip}; the list (GET /ip) lacks gateway, mask and broadcast.
type apiIP struct {
	apiTraffic
	IP           string            `json:"ip"`
	Gateway      client.FlexString `json:"gateway"`
	Mask         client.FlexInt    `json:"mask"`
	Broadcast    client.FlexString `json:"broadcast"`
	ServerIP     client.FlexString `json:"server_ip"`
	ServerNumber client.FlexInt    `json:"server_number"`
	Locked       bool              `json:"locked"`
	SeparateMAC  client.FlexString `json:"separate_mac"`
}

// ipBaseModel holds what both the single read and the list deliver.
type ipBaseModel struct {
	trafficSettings
	IP           types.String `tfsdk:"ip"`
	ServerIP     types.String `tfsdk:"server_ip"`
	ServerNumber types.Int64  `tfsdk:"server_number"`
	Locked       types.Bool   `tfsdk:"locked"`
	SeparateMAC  types.String `tfsdk:"separate_mac"`
}

func (m *ipBaseModel) fill(a apiIP) {
	m.trafficSettings.fill(a.apiTraffic)
	m.IP = types.StringValue(a.IP)
	m.ServerIP = flexStr(a.ServerIP)
	m.ServerNumber = flexInt(a.ServerNumber)
	m.Locked = types.BoolValue(a.Locked)
	m.SeparateMAC = flexStr(a.SeparateMAC)
}

type ipModel struct {
	ipBaseModel
	Gateway   types.String `tfsdk:"gateway"`
	Mask      types.Int64  `tfsdk:"mask"`
	Broadcast types.String `tfsdk:"broadcast"`
}

func (m *ipModel) fill(a apiIP) {
	m.ipBaseModel.fill(a)
	m.Gateway = flexStr(a.Gateway)
	m.Mask = flexInt(a.Mask)
	m.Broadcast = flexStr(a.Broadcast)
}

func ipPath(ip string) string { return "/ip/" + escape(ip) }

func readIP(ctx context.Context, c *client.Client, ip string) (apiIP, error) {
	return client.GetObject[apiIP](ctx, c, ipPath(ip), "ip", nil)
}

func (r *ipResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ip"
}

func (r *ipResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A single IPv4 address of a server, adopted to manage its traffic warnings. IPs cannot be " +
			"created here (order one with `hetznerrobot_server_addon_order`). Destroying the resource only forgets the IP; " +
			"cancel it with `hetznerrobot_ip_cancellation`. A server's IPv6 addresses are a /64 subnet: manage their " +
			"traffic warnings with `hetznerrobot_subnet`.",
		Attributes: mergeAttrs(trafficAttrs(), map[string]schema.Attribute{
			"ip":            reqStrNew("The IP address."),
			"gateway":       stableStr("Gateway."),
			"mask":          stableInt("Prefix length of the network the IP sits in."),
			"broadcast":     stableStr("Broadcast address."),
			"server_ip":     stableStr("Main IP address of the server the IP belongs to."),
			"server_number": stableInt("Server the IP belongs to."),
			"locked":        computedBool("Whether the IP is locked."),
			"separate_mac":  computedStr("Separate MAC address, if one is set (see `hetznerrobot_ip_mac`)."),
		}),
	}
}

func (r *ipResource) apply(ctx context.Context, plan *ipModel) error {
	if plan.configured() {
		if _, err := client.SendObject[apiIP](ctx, r.client, http.MethodPost, ipPath(plan.IP.ValueString()), "ip",
			plan.form()); err != nil {
			return err
		}
	}
	got, err := readIP(ctx, r.client, plan.IP.ValueString())
	if err != nil {
		return err
	}
	plan.fill(got)
	return nil
}

func (r *ipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error updating IP traffic warnings", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ipModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readIP(ctx, r.client, state.IP.ValueString())
	if client.NotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading IP", err.Error())
		return
	}
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *ipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ipModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error updating IP traffic warnings", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete only forgets the IP; cancelling it is hetznerrobot_ip_cancellation.
func (r *ipResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {}

func (r *ipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ip"), req, resp)
}

// ===================== separate MAC of a single IP =====================

var (
	_ resource.Resource                = (*ipMACResource)(nil)
	_ resource.ResourceWithImportState = (*ipMACResource)(nil)
)

// NewIPMACResource generates a separate MAC address for a single IP.
func NewIPMACResource() resource.Resource { return &ipMACResource{} }

type ipMACResource struct{ resourceBase }

type apiIPMAC struct {
	IP  string            `json:"ip"`
	MAC client.FlexString `json:"mac"`
}

type ipMACModel struct {
	IP  types.String `tfsdk:"ip"`
	MAC types.String `tfsdk:"mac"`
}

func ipMACPath(ip string) string { return ipPath(ip) + "/mac" }

func (r *ipMACResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ip_mac"
}

func (r *ipMACResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A separate MAC address for a single additional IP — needed to bridge it to a virtual machine. " +
			"Robot generates the address; destroying the resource removes it again. Limited to 10 changes per hour.",
		Attributes: map[string]schema.Attribute{
			"ip":  reqStrNew("The IP address."),
			"mac": stableStr("The generated MAC address."),
		},
	}
}

func (r *ipMACResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ipMACModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.SendObject[apiIPMAC](ctx, r.client, http.MethodPut, ipMACPath(plan.IP.ValueString()), "mac", url.Values{})
	if err != nil {
		resp.Diagnostics.AddError("Error generating separate MAC address", err.Error())
		return
	}
	plan.MAC = flexStr(got.MAC)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *ipMACResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ipMACModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiIPMAC](ctx, r.client, ipMACPath(state.IP.ValueString()), "mac", nil)
	if client.NotFound(err) { // MAC_NOT_FOUND, MAC_NOT_AVAILABLE or IP_NOT_FOUND
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading separate MAC address", err.Error())
		return
	}
	if !got.MAC.Valid {
		resp.State.RemoveResource(ctx)
		return
	}
	state.MAC = flexStr(got.MAC)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never called: the only argument forces replacement.
func (r *ipMACResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {}

func (r *ipMACResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ipMACModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodDelete, ipMACPath(state.IP.ValueString()), nil, nil)
	if err != nil && !client.NotFound(err) && !client.HasCode(err, "MAC_NOT_FOUND") {
		resp.Diagnostics.AddError("Error removing separate MAC address", err.Error())
	}
}

func (r *ipMACResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ip"), req, resp)
}

// ===================== data sources =====================

func ipBaseDSAttrs() map[string]dschema.Attribute {
	return mergeAttrs(trafficDSAttrs(), map[string]dschema.Attribute{
		"ip":            dsStr("The IP address."),
		"server_ip":     dsStr("Main IP address of the server the IP belongs to."),
		"server_number": dsInt("Server the IP belongs to."),
		"locked":        dsBool("Whether the IP is locked."),
		"separate_mac":  dsStr("Separate MAC address, if one is set."),
	})
}

// NewIPDataSource reads one single IP address.
func NewIPDataSource() datasource.DataSource { return &ipDataSource{} }

type ipDataSource struct{ dataSourceBase }

func (d *ipDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ip"
}

func (d *ipDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads one single IPv4 address of the account. IPv6 addresses are subnets — see `hetznerrobot_subnet`.",
		Attributes: with(ipBaseDSAttrs(), map[string]dschema.Attribute{
			"ip":        dsRequiredStr("The IP address."),
			"gateway":   dsStr("Gateway."),
			"mask":      dsInt("Prefix length of the network the IP sits in."),
			"broadcast": dsStr("Broadcast address."),
		}),
	}
}

func (d *ipDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg ipModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readIP(ctx, d.client, cfg.IP.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading IP", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewIPsDataSource lists the single IP addresses of the account or a server.
func NewIPsDataSource() datasource.DataSource { return &ipsDataSource{} }

type ipsDataSource struct{ dataSourceBase }

type ipsModel struct {
	ServerIP types.String  `tfsdk:"server_ip"`
	IPs      []ipBaseModel `tfsdk:"ips"`
}

func (d *ipsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ips"
}

func (d *ipsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the single IPv4 addresses of the account, or of one server. IPv6 addresses are subnets — see `hetznerrobot_subnets`.",
		Attributes: map[string]dschema.Attribute{
			"server_ip": dsOptionalStr("Main IP address of a server; only its IPs are listed."),
			"ips":       dsList("The IP addresses.", ipBaseDSAttrs()),
		},
	}
}

func (d *ipsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg ipsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := url.Values{}
	formStr(query, "server_ip", cfg.ServerIP)
	list, err := client.GetList[apiIP](ctx, d.client, "/ip", "ip", query)
	if err != nil {
		resp.Diagnostics.AddError("Error listing IPs", err.Error())
		return
	}
	cfg.IPs = make([]ipBaseModel, len(list))
	for i, a := range list {
		cfg.IPs[i].fill(a)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
