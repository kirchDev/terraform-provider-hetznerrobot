package provider

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

var (
	_ resource.Resource                = (*failoverResource)(nil)
	_ resource.ResourceWithImportState = (*failoverResource)(nil)
)

// failoverLockedTimeout bounds how long a switch waits for another one that
// is still in progress (FAILOVER_LOCKED).
const failoverLockedTimeout = 2 * time.Minute

// NewFailoverResource manages the routing of a failover IP.
func NewFailoverResource() resource.Resource { return &failoverResource{} }

type failoverResource struct{ resourceBase }

type apiFailover struct {
	IP             string            `json:"ip"`
	Netmask        client.FlexString `json:"netmask"`
	ServerIP       client.FlexString `json:"server_ip"`
	ServerIPv6Net  client.FlexString `json:"server_ipv6_net"`
	ServerNumber   client.FlexInt    `json:"server_number"`
	ActiveServerIP client.FlexString `json:"active_server_ip"`
}

type failoverModel struct {
	IP             types.String `tfsdk:"ip"`
	ActiveServerIP types.String `tfsdk:"active_server_ip"`
	Netmask        types.String `tfsdk:"netmask"`
	ServerIP       types.String `tfsdk:"server_ip"`
	ServerIPv6Net  types.String `tfsdk:"server_ipv6_net"`
	ServerNumber   types.Int64  `tfsdk:"server_number"`
}

func (m *failoverModel) fill(f apiFailover) {
	m.IP = types.StringValue(f.IP)
	m.ActiveServerIP = flexStr(f.ActiveServerIP)
	m.Netmask = flexStr(f.Netmask)
	m.ServerIP = flexStr(f.ServerIP)
	m.ServerIPv6Net = flexStr(f.ServerIPv6Net)
	m.ServerNumber = flexInt(f.ServerNumber)
}

func failoverPath(ip string) string { return "/failover/" + escape(ip) }

func readFailover(ctx context.Context, c *client.Client, ip string) (apiFailover, error) {
	return client.GetObject[apiFailover](ctx, c, failoverPath(ip), "failover", nil)
}

// whileLocked repeats a failover write while another switch of the same IP is
// still running (409 FAILOVER_LOCKED).
func whileLocked(ctx context.Context, write func() error) error {
	var last error
	err := waitFor(ctx, failoverLockedTimeout, "the running failover switch to finish", func() (bool, error) {
		last = write()
		if client.HasCode(last, "FAILOVER_LOCKED") {
			return false, nil
		}
		return true, last
	})
	if err != nil && last != nil && client.HasCode(last, "FAILOVER_LOCKED") {
		return last
	}
	return err
}

func (r *failoverResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_failover"
}

func (r *failoverResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Routes a failover IP (or failover subnet) to a server. The IP must be configured on every server " +
			"it can be switched to. **Destroying the resource deletes the routing** — the failover IP then reaches no " +
			"server until it is routed again.",
		Attributes: map[string]schema.Attribute{
			"ip": reqStrNew("Failover IP, or net address of a failover subnet."),
			"active_server_ip": reqStr("Main IP of the server the failover IP is routed to. For an IPv6 failover subnet, the " +
				"net address of the target server's main IPv6 subnet."),
			"netmask":         stableStr("Netmask of the failover IP."),
			"server_ip":       stableStr("Main IP of the server the failover IP was ordered for."),
			"server_ipv6_net": stableStr("Main IPv6 net of that server."),
			"server_number":   stableInt("Number of that server."),
		},
	}
}

func (r *failoverResource) route(ctx context.Context, ip, target string) error {
	return whileLocked(ctx, func() error {
		_, err := client.SendObject[apiFailover](ctx, r.client, http.MethodPost, failoverPath(ip), "failover",
			url.Values{"active_server_ip": {target}})
		if client.HasCode(err, "FAILOVER_ALREADY_ROUTED") {
			return nil
		}
		return err
	})
}

func (r *failoverResource) apply(ctx context.Context, plan *failoverModel) error {
	ip, target := plan.IP.ValueString(), plan.ActiveServerIP.ValueString()
	cur, err := readFailover(ctx, r.client, ip)
	if err != nil {
		return err
	}
	if !cur.ActiveServerIP.Valid || cur.ActiveServerIP.Value != target {
		if err := r.route(ctx, ip, target); err != nil {
			return err
		}
		if cur, err = readFailover(ctx, r.client, ip); err != nil {
			return err
		}
	}
	plan.fill(cur)
	return nil
}

func (r *failoverResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan failoverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error routing failover IP", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *failoverResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state failoverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readFailover(ctx, r.client, state.IP.ValueString())
	if client.NotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading failover IP", err.Error())
		return
	}
	// An unrouted failover IP reads as null, so the next plan routes it again.
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *failoverResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan failoverModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.apply(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error switching failover IP", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *failoverResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state failoverModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := whileLocked(ctx, func() error {
		return r.client.Send(ctx, http.MethodDelete, failoverPath(state.IP.ValueString()), nil, nil)
	})
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error deleting failover routing", err.Error())
	}
}

func (r *failoverResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ip"), req, resp)
}

// --- data sources ---

func failoverDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"ip":               dsStr("Failover IP, or net address of a failover subnet."),
		"active_server_ip": dsStr("Main IP of the server the failover IP is routed to; null when unrouted."),
		"netmask":          dsStr("Netmask of the failover IP."),
		"server_ip":        dsStr("Main IP of the server the failover IP was ordered for."),
		"server_ipv6_net":  dsStr("Main IPv6 net of that server."),
		"server_number":    dsInt("Number of that server."),
	}
}

// NewFailoverDataSource reads one failover IP.
func NewFailoverDataSource() datasource.DataSource { return &failoverDataSource{} }

type failoverDataSource struct{ dataSourceBase }

func (d *failoverDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_failover"
}

func (d *failoverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads one failover IP and where it is routed to.",
		Attributes: with(failoverDSAttrs(), map[string]dschema.Attribute{
			"ip": dsRequiredStr("Failover IP, or net address of a failover subnet."),
		}),
	}
}

func (d *failoverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg failoverModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readFailover(ctx, d.client, cfg.IP.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading failover IP", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewFailoversDataSource lists every failover IP of the account.
func NewFailoversDataSource() datasource.DataSource { return &failoversDataSource{} }

type failoversDataSource struct{ dataSourceBase }

type failoversModel struct {
	Failovers []failoverModel `tfsdk:"failovers"`
}

func (d *failoversDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_failovers"
}

func (d *failoversDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists every failover IP of the account and where each is routed to.",
		Attributes: map[string]dschema.Attribute{
			"failovers": dsList("The failover IPs.", failoverDSAttrs()),
		},
	}
}

func (d *failoversDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := client.GetList[apiFailover](ctx, d.client, "/failover", "failover", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error listing failover IPs", err.Error())
		return
	}
	out := failoversModel{Failovers: make([]failoverModel, len(list))}
	for i, f := range list {
		out.Failovers[i].fill(f)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}
