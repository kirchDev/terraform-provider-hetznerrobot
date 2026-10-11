package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

// Cancellations of servers, single IPs and subnets. Each is a resource whose
// existence is the pending cancellation: creating it cancels, destroying it
// withdraws the cancellation again (as long as Robot still allows that).

var cancellationDatePattern = regexp.MustCompile(`^(now|\d{4}-\d{2}-\d{2})$`)

// cancellationDateAttr is the configured date. The API answers "now" with the
// actual date, so the configured value is kept in state and the API's date is
// exposed as effective_date.
func cancellationDateAttr(what string) schema.StringAttribute {
	a := reqStrNew(fmt.Sprintf("Date the %s should be cancelled to (`yyyy-MM-dd`), or `now` to cancel immediately. "+
		"See `earliest_cancellation_date` of the data source for the earliest possible date.", what))
	a.Validators = []validator.String{stringvalidator.RegexMatches(cancellationDatePattern, "must be `now` or a date in yyyy-MM-dd format")}
	return a
}

// apiCancellation is the "cancellation" object of all three endpoints. The
// date key is spelled "cancellation_date" in the documented tables but
// "cancellation-date" in the IP and subnet examples; both are accepted.
type apiCancellation struct {
	ServerIP                 client.FlexString   `json:"server_ip"`
	ServerIPv6Net            client.FlexString   `json:"server_ipv6_net"`
	ServerNumber             client.FlexInt      `json:"server_number"`
	ServerName               client.FlexString   `json:"server_name"`
	IP                       client.FlexString   `json:"ip"`
	Mask                     client.FlexString   `json:"mask"`
	EarliestCancellationDate client.FlexString   `json:"earliest_cancellation_date"`
	Cancelled                bool                `json:"cancelled"`
	ReservationPossible      bool                `json:"reservation_possible"`
	Reserved                 bool                `json:"reserved"`
	CancellationDate         client.FlexString   `json:"cancellation_date"`
	CancellationDateDash     client.FlexString   `json:"cancellation-date"`
	CancellationReason       client.StringOrList `json:"cancellation_reason"`
}

func (c apiCancellation) date() types.String {
	if c.CancellationDate.Valid {
		return flexStr(c.CancellationDate)
	}
	return flexStr(c.CancellationDateDash)
}

// reason is the chosen reason of an active cancellation; while none is
// active the field carries the list of possible reasons instead.
func (c apiCancellation) reason() types.String {
	if c.CancellationReason.IsList || c.CancellationReason.Single == "" {
		return types.StringNull()
	}
	return types.StringValue(c.CancellationReason.Single)
}

// ===================== server cancellation =====================

var (
	_ resource.Resource                = (*serverCancellationResource)(nil)
	_ resource.ResourceWithImportState = (*serverCancellationResource)(nil)
)

// NewServerCancellationResource cancels a dedicated server.
func NewServerCancellationResource() resource.Resource { return &serverCancellationResource{} }

type serverCancellationResource struct{ resourceBase }

type serverCancellationModel struct {
	ServerNumber             types.Int64  `tfsdk:"server_number"`
	CancellationDate         types.String `tfsdk:"cancellation_date"`
	CancellationReason       types.String `tfsdk:"cancellation_reason"`
	ReserveLocation          types.Bool   `tfsdk:"reserve_location"`
	EffectiveDate            types.String `tfsdk:"effective_date"`
	EarliestCancellationDate types.String `tfsdk:"earliest_cancellation_date"`
	Cancelled                types.Bool   `tfsdk:"cancelled"`
	Reserved                 types.Bool   `tfsdk:"reserved"`
	ReservationPossible      types.Bool   `tfsdk:"reservation_possible"`
	ServerIP                 types.String `tfsdk:"server_ip"`
	ServerName               types.String `tfsdk:"server_name"`
}

// fill keeps the configured date and reason; only an import (no value yet)
// takes them from the API.
func (m *serverCancellationModel) fill(c apiCancellation) {
	importing := m.CancellationDate.IsNull() || m.CancellationDate.IsUnknown()
	m.ServerNumber = types.Int64Value(c.ServerNumber.Value)
	if m.CancellationDate.IsNull() || m.CancellationDate.IsUnknown() {
		m.CancellationDate = c.date()
	}
	if m.CancellationReason.IsNull() || m.CancellationReason.IsUnknown() {
		m.CancellationReason = c.reason()
	}
	// An import learns a reservation from `reserved`; without one, null and
	// false mean the same, and null matches a configuration that leaves it out.
	if importing && c.Reserved {
		m.ReserveLocation = types.BoolValue(true)
	}
	m.EffectiveDate = c.date()
	m.EarliestCancellationDate = flexStr(c.EarliestCancellationDate)
	m.Cancelled = types.BoolValue(c.Cancelled)
	m.Reserved = types.BoolValue(c.Reserved)
	m.ReservationPossible = types.BoolValue(c.ReservationPossible)
	m.ServerIP = flexStr(c.ServerIP)
	m.ServerName = flexStr(c.ServerName)
}

func serverCancellationPath(n int64) string { return fmt.Sprintf("/server/%d/cancellation", n) }

func (r *serverCancellationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_cancellation"
}

func (r *serverCancellationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Cancels a dedicated server. **A server cancelled `now` is gone immediately, with all its data** — " +
			"prefer a date. Destroying this resource withdraws a pending cancellation; once the date has passed there " +
			"is nothing left to withdraw, and the resource stays in state as the record of it.",
		Attributes: map[string]schema.Attribute{
			"server_number":     reqIntNew("Server number."),
			"cancellation_date": cancellationDateAttr("server"),
			"cancellation_reason": schema.StringAttribute{
				MarkdownDescription: "Reason for the cancellation — one of the `cancellation_reasons` of the `hetznerrobot_server_cancellation` data source. " +
					"Changing it forces a new cancellation; setting or removing it does not.",
				Optional:      true,
				PlanModifiers: []planmodifier.String{replaceOnSetChange{}},
			},
			"reserve_location": schema.BoolAttribute{
				MarkdownDescription: "Whether the server's location should be reserved after the cancellation. Robot " +
					"requires it when `reservation_possible` is true and only accepts `false` otherwise. " +
					"Changing it forces a new cancellation; setting or removing it does not.",
				Optional:      true,
				PlanModifiers: []planmodifier.Bool{replaceOnSetChange{}},
			},
			"effective_date":             computedStr("Cancellation date as Robot holds it — the actual date for `now`."),
			"earliest_cancellation_date": computedStr("Earliest possible cancellation date."),
			"cancelled":                  computedBool("Whether the cancellation is active."),
			"reserved":                   computedBool("Whether the location will be reserved after the cancellation."),
			"reservation_possible":       computedBool("Whether the location can be reserved."),
			"server_ip":                  computedStr("Main IP address of the server."),
			"server_name":                computedStr("Name of the server."),
		},
	}
}

func (r *serverCancellationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverCancellationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	form := url.Values{"cancellation_date": {plan.CancellationDate.ValueString()}}
	formStr(form, "cancellation_reason", plan.CancellationReason)
	formBool(form, "reserve_location", plan.ReserveLocation)
	got, err := client.SendObject[apiCancellation](ctx, r.client, http.MethodPost,
		serverCancellationPath(plan.ServerNumber.ValueInt64()), "cancellation", form)
	if err != nil {
		resp.Diagnostics.AddError("Error cancelling server", err.Error())
		return
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverCancellationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverCancellationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiCancellation](ctx, r.client, serverCancellationPath(state.ServerNumber.ValueInt64()), "cancellation", nil)
	if client.NotFound(err) {
		// The server is gone — cancelled "now", or its date has passed. That is
		// the cancellation done, not undone: removing it would plan a new one
		// against a server that no longer exists.
		if !state.Cancelled.ValueBool() {
			resp.State.RemoveResource(ctx)
		}
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading server cancellation", err.Error())
		return
	}
	if !got.Cancelled { // withdrawn out of band
		resp.State.RemoveResource(ctx)
		return
	}
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only records a reason or reserve_location set or unset after the fact
// (an import cannot always read them back): Robot has nothing to change.
func (r *serverCancellationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serverCancellationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.CancellationReason, state.ReserveLocation = plan.CancellationReason, plan.ReserveLocation
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serverCancellationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverCancellationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodDelete, serverCancellationPath(state.ServerNumber.ValueInt64()), nil, nil)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error withdrawing server cancellation", err.Error())
	}
}

func (r *serverCancellationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := parseInt64(req.ID, "server_number")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_number"), n)...)
}

// NewServerCancellationDataSource reads the cancellation data of a server.
func NewServerCancellationDataSource() datasource.DataSource { return &serverCancellationDataSource{} }

type serverCancellationDataSource struct{ dataSourceBase }

type serverCancellationDSModel struct {
	ServerNumber             types.Int64  `tfsdk:"server_number"`
	ServerIP                 types.String `tfsdk:"server_ip"`
	ServerName               types.String `tfsdk:"server_name"`
	EarliestCancellationDate types.String `tfsdk:"earliest_cancellation_date"`
	Cancelled                types.Bool   `tfsdk:"cancelled"`
	ReservationPossible      types.Bool   `tfsdk:"reservation_possible"`
	Reserved                 types.Bool   `tfsdk:"reserved"`
	CancellationDate         types.String `tfsdk:"cancellation_date"`
	CancellationReasons      types.List   `tfsdk:"cancellation_reasons"`
	CancellationReason       types.String `tfsdk:"cancellation_reason"`
}

func (d *serverCancellationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_cancellation"
}

func (d *serverCancellationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads the cancellation data of a server: the earliest possible date, the accepted reasons and " +
			"whether a cancellation is active.",
		Attributes: map[string]dschema.Attribute{
			"server_number":              dsRequiredInt("Server number."),
			"server_ip":                  dsStr("Main IP address of the server."),
			"server_name":                dsStr("Name of the server."),
			"earliest_cancellation_date": dsStr("Earliest possible cancellation date."),
			"cancelled":                  dsBool("Whether a cancellation is active."),
			"reservation_possible":       dsBool("Whether the location can be reserved after a cancellation."),
			"reserved":                   dsBool("Whether the location will be reserved."),
			"cancellation_date":          dsStr("Date of the active cancellation; null when none is active."),
			"cancellation_reasons":       dsStrList("Possible cancellation reasons while none is active; the chosen one once it is."),
			"cancellation_reason":        dsStr("Reason of the active cancellation; null when none is active."),
		},
	}
}

func (d *serverCancellationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg serverCancellationDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := client.GetObject[apiCancellation](ctx, d.client, serverCancellationPath(cfg.ServerNumber.ValueInt64()), "cancellation", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error reading server cancellation", err.Error())
		return
	}
	cfg.ServerIP = flexStr(c.ServerIP)
	cfg.ServerName = flexStr(c.ServerName)
	cfg.EarliestCancellationDate = flexStr(c.EarliestCancellationDate)
	cfg.Cancelled = types.BoolValue(c.Cancelled)
	cfg.ReservationPossible = types.BoolValue(c.ReservationPossible)
	cfg.Reserved = types.BoolValue(c.Reserved)
	cfg.CancellationDate = c.date()
	cfg.CancellationReasons = stringList(c.CancellationReason.Values())
	cfg.CancellationReason = c.reason()
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// ===================== IP and subnet cancellation =====================

// addrCancellationKind names what differs between the IP and the subnet
// cancellation; the API behind both is the same.
type addrCancellationKind struct {
	typeName string // "ip_cancellation"
	what     string // "IP address"
	base     string // "/ip"
	subnet   bool
}

var (
	ipCancellationKind     = addrCancellationKind{typeName: "ip_cancellation", what: "IP address", base: "/ip"}
	subnetCancellationKind = addrCancellationKind{typeName: "subnet_cancellation", what: "subnet", base: "/subnet", subnet: true}
)

func (k addrCancellationKind) path(ip string) string {
	return k.base + "/" + escape(ip) + "/cancellation"
}

func (k addrCancellationKind) read(ctx context.Context, c *client.Client, ip string) (apiCancellation, error) {
	return client.GetObject[apiCancellation](ctx, c, k.path(ip), "cancellation", nil)
}

// addrCancellationModel serves both resources; mask stays null for an IP.
type addrCancellationModel struct {
	IP                       types.String `tfsdk:"ip"`
	CancellationDate         types.String `tfsdk:"cancellation_date"`
	EffectiveDate            types.String `tfsdk:"effective_date"`
	EarliestCancellationDate types.String `tfsdk:"earliest_cancellation_date"`
	Cancelled                types.Bool   `tfsdk:"cancelled"`
	ServerNumber             types.Int64  `tfsdk:"server_number"`
}

func (m *addrCancellationModel) fill(c apiCancellation) {
	if m.IP.IsNull() || m.IP.IsUnknown() {
		m.IP = flexStr(c.IP)
	}
	if m.CancellationDate.IsNull() || m.CancellationDate.IsUnknown() {
		m.CancellationDate = c.date()
	}
	m.EffectiveDate = c.date()
	m.EarliestCancellationDate = flexStr(c.EarliestCancellationDate)
	m.Cancelled = types.BoolValue(c.Cancelled)
	m.ServerNumber = flexInt(c.ServerNumber)
}

type subnetCancellationModel struct {
	addrCancellationModel
	Mask types.String `tfsdk:"mask"`
}

// addrCancellationResource implements both cancellations over a model that
// is the IP model, or the IP model plus mask.
type addrCancellationResource struct {
	resourceBase
	kind addrCancellationKind
}

var (
	_ resource.Resource                = (*addrCancellationResource)(nil)
	_ resource.ResourceWithImportState = (*addrCancellationResource)(nil)
)

// NewIPCancellationResource cancels a single additional IP address.
func NewIPCancellationResource() resource.Resource {
	return &addrCancellationResource{kind: ipCancellationKind}
}

// NewSubnetCancellationResource cancels a subnet.
func NewSubnetCancellationResource() resource.Resource {
	return &addrCancellationResource{kind: subnetCancellationKind}
}

func (r *addrCancellationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + r.kind.typeName
}

func (r *addrCancellationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	w := r.kind.what
	attrs := map[string]schema.Attribute{
		"ip":                         reqStrNew(fmt.Sprintf("The %s to cancel (for a subnet: its net address).", w)),
		"cancellation_date":          cancellationDateAttr(w),
		"effective_date":             computedStr("Cancellation date as Robot holds it — the actual date for `now`."),
		"earliest_cancellation_date": computedStr("Earliest possible cancellation date."),
		"cancelled":                  computedBool("Whether the cancellation is active."),
		"server_number":              computedInt("Server the " + w + " belongs to."),
	}
	if r.kind.subnet {
		attrs["mask"] = computedStr("Prefix length of the subnet.")
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: fmt.Sprintf("Cancels a %s. Destroying this resource withdraws a pending cancellation; "+
			"one cancelled `now` is gone immediately. Every argument forces a new cancellation.", w),
		Attributes: attrs,
	}
}

func (r *addrCancellationResource) get(ctx context.Context, src interface {
	Get(context.Context, any) diag.Diagnostics
}, diags *diag.Diagnostics) (*subnetCancellationModel, bool) {
	var m subnetCancellationModel
	if r.kind.subnet {
		diags.Append(src.Get(ctx, &m)...)
	} else {
		diags.Append(src.Get(ctx, &m.addrCancellationModel)...)
	}
	return &m, !diags.HasError()
}

func (r *addrCancellationResource) set(ctx context.Context, dst interface {
	Set(context.Context, any) diag.Diagnostics
}, m *subnetCancellationModel, c apiCancellation, diags *diag.Diagnostics) {
	m.fill(c)
	if r.kind.subnet {
		m.Mask = flexStr(c.Mask)
		diags.Append(dst.Set(ctx, m)...)
		return
	}
	diags.Append(dst.Set(ctx, &m.addrCancellationModel)...)
}

func (r *addrCancellationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	plan, ok := r.get(ctx, req.Plan, &resp.Diagnostics)
	if !ok {
		return
	}
	got, err := client.SendObject[apiCancellation](ctx, r.client, http.MethodPost, r.kind.path(plan.IP.ValueString()), "cancellation",
		url.Values{"cancellation_date": {plan.CancellationDate.ValueString()}})
	if err != nil {
		resp.Diagnostics.AddError("Error cancelling "+r.kind.what, err.Error())
		return
	}
	r.set(ctx, &resp.State, plan, got, &resp.Diagnostics)
}

func (r *addrCancellationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	state, ok := r.get(ctx, req.State, &resp.Diagnostics)
	if !ok {
		return
	}
	got, err := r.kind.read(ctx, r.client, state.IP.ValueString())
	if client.NotFound(err) {
		// Gone after the cancellation took effect: done, not undone.
		if !state.Cancelled.ValueBool() {
			resp.State.RemoveResource(ctx)
		}
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading "+r.kind.what+" cancellation", err.Error())
		return
	}
	if !got.Cancelled { // withdrawn out of band
		resp.State.RemoveResource(ctx)
		return
	}
	r.set(ctx, &resp.State, state, got, &resp.Diagnostics)
}

// Update is never called: every argument forces replacement.
func (r *addrCancellationResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {
}

func (r *addrCancellationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	state, ok := r.get(ctx, req.State, &resp.Diagnostics)
	if !ok {
		return
	}
	err := r.client.Send(ctx, http.MethodDelete, r.kind.path(state.IP.ValueString()), nil, nil)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error withdrawing "+r.kind.what+" cancellation", err.Error())
	}
}

func (r *addrCancellationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("ip"), req, resp)
}

// --- data sources ---

type addrCancellationDataSource struct {
	dataSourceBase
	kind addrCancellationKind
}

// NewIPCancellationDataSource reads the cancellation data of a single IP.
func NewIPCancellationDataSource() datasource.DataSource {
	return &addrCancellationDataSource{kind: ipCancellationKind}
}

// NewSubnetCancellationDataSource reads the cancellation data of a subnet.
func NewSubnetCancellationDataSource() datasource.DataSource {
	return &addrCancellationDataSource{kind: subnetCancellationKind}
}

type addrCancellationDSModel struct {
	IP                       types.String `tfsdk:"ip"`
	ServerNumber             types.Int64  `tfsdk:"server_number"`
	EarliestCancellationDate types.String `tfsdk:"earliest_cancellation_date"`
	Cancelled                types.Bool   `tfsdk:"cancelled"`
	CancellationDate         types.String `tfsdk:"cancellation_date"`
}

type subnetCancellationDSModel struct {
	addrCancellationDSModel
	Mask types.String `tfsdk:"mask"`
}

func (d *addrCancellationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + d.kind.typeName
}

func (d *addrCancellationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	w := d.kind.what
	attrs := map[string]dschema.Attribute{
		"ip":                         dsRequiredStr(fmt.Sprintf("The %s (for a subnet: its net address).", w)),
		"server_number":              dsInt("Server the " + w + " belongs to."),
		"earliest_cancellation_date": dsStr("Earliest possible cancellation date."),
		"cancelled":                  dsBool("Whether a cancellation is active."),
		"cancellation_date":          dsStr("Date of the active cancellation; null when none is active."),
	}
	if d.kind.subnet {
		attrs["mask"] = dsStr("Prefix length of the subnet.")
	}
	resp.Schema = dschema.Schema{
		MarkdownDescription: fmt.Sprintf("Reads the cancellation data of a %s. Fails when the %s cannot be cancelled "+
			"(e.g. a server's main IP).", w, w),
		Attributes: attrs,
	}
}

func (d *addrCancellationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg subnetCancellationDSModel
	if d.kind.subnet {
		resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	} else {
		resp.Diagnostics.Append(req.Config.Get(ctx, &cfg.addrCancellationDSModel)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.kind.read(ctx, d.client, cfg.IP.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading "+d.kind.what+" cancellation", err.Error())
		return
	}
	cfg.ServerNumber = flexInt(c.ServerNumber)
	cfg.EarliestCancellationDate = flexStr(c.EarliestCancellationDate)
	cfg.Cancelled = types.BoolValue(c.Cancelled)
	cfg.CancellationDate = c.date()
	if d.kind.subnet {
		cfg.Mask = flexStr(c.Mask)
		resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg.addrCancellationDSModel)...)
}
