package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

// An order is the one place in this provider where a mistake costs money: every
// POST places a real, billed order (20 per day at most). Two rules follow.
//
//   - Once the POST succeeded, Create always saves the state. Waiting for the
//     transaction to become ready may time out or hit a transient error; that is
//     a warning, never an error — an error would taint the resource and the next
//     apply would order a second server.
//   - Destroying an order only forgets it. The server (or the IP) keeps running
//     and is billed until it is cancelled with the matching cancellation
//     resource.
//
// Robot keeps transactions for 30 days. A transaction that is gone keeps its
// state as it is; an order is never dropped from state by a refresh.

const orderCreateTimeout = 3 * time.Hour

var (
	_ resource.Resource                   = (*serverOrderResource[serverOrderModel, *serverOrderModel])(nil)
	_ resource.ResourceWithImportState    = (*serverOrderResource[serverOrderModel, *serverOrderModel])(nil)
	_ resource.ResourceWithValidateConfig = (*serverOrderResource[serverMarketOrderModel, *serverMarketOrderModel])(nil)
	_ resource.Resource                   = (*serverAddonOrderResource)(nil)
	_ resource.ResourceWithImportState    = (*serverAddonOrderResource)(nil)
)

type apiOrderKey struct {
	Key struct {
		Name        string `json:"name"`
		Fingerprint string `json:"fingerprint"`
	} `json:"key"`
}

type apiOrderTransaction struct {
	ID            string            `json:"id"`
	Date          client.FlexString `json:"date"`
	Status        string            `json:"status"`
	ServerNumber  client.FlexInt    `json:"server_number"`
	ServerIP      client.FlexString `json:"server_ip"`
	AuthorizedKey []apiOrderKey     `json:"authorized_key"`
	HostKey       []apiOrderKey     `json:"host_key"`
	Comment       client.FlexString `json:"comment"`
	Product       struct {
		ID       client.FlexString `json:"id"`
		Location client.FlexString `json:"location"`
	} `json:"product"`
	Addons []string `json:"addons"`
}

// orderStatusReady and orderStatusCancelled are the end states of a
// transaction; "in process" is the only other one.
const (
	orderStatusReady     = "ready"
	orderStatusCancelled = "cancelled"
)

// orderReplaceOnChange replaces an order only when an argument that was set
// changes to another set value. Setting an argument that was unset (after an
// import, which cannot read the password, the keys or the comment back) or
// unsetting one never places a new order — the server already exists, and the
// argument only described how it was ordered. A null test counts as false, so
// switching a test order to a real one does order.
type orderReplaceOnChange struct{}

func (orderReplaceOnChange) Description(context.Context) string {
	return "Places a new order when a set value changes to another set value."
}

func (m orderReplaceOnChange) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (orderReplaceOnChange) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	resp.RequiresReplace = !req.PlanValue.Equal(req.StateValue)
}

func (orderReplaceOnChange) PlanModifySet(_ context.Context, req planmodifier.SetRequest, resp *planmodifier.SetResponse) {
	if req.State.Raw.IsNull() || req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	resp.RequiresReplace = !req.PlanValue.Equal(req.StateValue)
}

func (orderReplaceOnChange) PlanModifyBool(_ context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	if req.State.Raw.IsNull() || req.StateValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	resp.RequiresReplace = req.PlanValue.ValueBool() != req.StateValue.ValueBool()
}

func orderOptStr(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		PlanModifiers:       []planmodifier.String{orderReplaceOnChange{}},
	}
}

func orderOptSet(desc string) schema.SetAttribute {
	return schema.SetAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		ElementType:         types.StringType,
		PlanModifiers:       []planmodifier.Set{orderReplaceOnChange{}},
	}
}

func orderTestAttr() schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "When true, Robot validates the order but does not process it — nothing is ordered or billed, " +
			"and no server number is ever assigned.",
		Optional:      true,
		PlanModifiers: []planmodifier.Bool{orderReplaceOnChange{}},
	}
}

const orderNotes = "\n\n~> **Destroying this resource does not cancel anything.** It only removes the order from state; " +
	"what was ordered keeps running and is billed until it is cancelled.\n\n" +
	"Ordering through the webservice has to be enabled in Robot first (Administration → Settings → Web service settings → Ordering). " +
	"Robot accepts 20 orders per day; prices are in the account's currency (`hetznerrobot_currency`). " +
	"Create waits for the transaction to become `ready` (see `timeouts`). When the wait ends early the order stays placed: " +
	"the apply finishes with a warning, and a later refresh fills in what is still missing — it never orders twice. " +
	"Robot keeps transactions for 30 days; after that the state simply stays as it is."

// --- server and server market orders ---

// serverOrderCommon holds what both server orders share; the two models embed
// it next to their own product id.
type serverOrderCommon struct {
	Dist           types.String   `tfsdk:"dist"`
	Lang           types.String   `tfsdk:"lang"`
	AuthorizedKeys types.Set      `tfsdk:"authorized_keys"`
	Password       types.String   `tfsdk:"password"`
	Comment        types.String   `tfsdk:"comment"`
	Addons         types.Set      `tfsdk:"addons"`
	Test           types.Bool     `tfsdk:"test"`
	TransactionID  types.String   `tfsdk:"transaction_id"`
	Date           types.String   `tfsdk:"date"`
	Status         types.String   `tfsdk:"status"`
	ServerNumber   types.Int64    `tfsdk:"server_number"`
	ServerIP       types.String   `tfsdk:"server_ip"`
	HostKeys       types.List     `tfsdk:"host_keys"`
	Timeouts       timeouts.Value `tfsdk:"timeouts"`
}

func (c *serverOrderCommon) fillTransaction(tx apiOrderTransaction) {
	c.TransactionID = types.StringValue(tx.ID)
	c.Date = flexStr(tx.Date)
	c.Status = types.StringValue(tx.Status)
	c.ServerNumber = flexInt(tx.ServerNumber)
	c.ServerIP = flexStr(tx.ServerIP)
	keys := make([]string, 0, len(tx.HostKey))
	for _, k := range tx.HostKey {
		keys = append(keys, k.Key.Fingerprint)
	}
	c.HostKeys = stringList(keys)
}

func (c *serverOrderCommon) form(ctx context.Context, f url.Values, diags *diag.Diagnostics) {
	formStr(f, "dist", c.Dist)
	formStr(f, "lang", c.Lang)
	formArray(f, "authorized_key", valueStrings(ctx, c.AuthorizedKeys, diags))
	formStr(f, "password", c.Password)
	formStr(f, "comment", c.Comment)
	formArray(f, "addon", valueStrings(ctx, c.Addons, diags))
	if c.Test.ValueBool() {
		f.Set("test", "true")
	}
}

type serverOrderModel struct {
	ProductID types.String `tfsdk:"product_id"`
	Location  types.String `tfsdk:"location"`
	serverOrderCommon
}

func (m *serverOrderModel) common() *serverOrderCommon { return &m.serverOrderCommon }

func (m *serverOrderModel) putProduct(f url.Values) {
	f.Set("product_id", m.ProductID.ValueString())
	formStr(f, "location", m.Location)
}

func (m *serverOrderModel) importProduct(tx apiOrderTransaction) {
	m.ProductID = types.StringValue(tx.Product.ID.Value)
}

type serverMarketOrderModel struct {
	ProductID types.Int64 `tfsdk:"product_id"`
	serverOrderCommon
}

func (m *serverMarketOrderModel) common() *serverOrderCommon { return &m.serverOrderCommon }

func (m *serverMarketOrderModel) putProduct(f url.Values) {
	f.Set("product_id", strconv.FormatInt(m.ProductID.ValueInt64(), 10))
}

func (m *serverMarketOrderModel) importProduct(tx apiOrderTransaction) {
	if n, err := strconv.ParseInt(tx.Product.ID.Value, 10, 64); err == nil {
		m.ProductID = types.Int64Value(n)
	}
}

// serverOrderModelPtr is what the shared implementation needs from a model.
type serverOrderModelPtr[M any] interface {
	*M
	common() *serverOrderCommon
	putProduct(url.Values)
	importProduct(apiOrderTransaction)
}

// serverOrderKind names what differs between the standard and the market order.
type serverOrderKind struct {
	typeSuffix  string
	collection  string
	description string
	productAttr func() map[string]schema.Attribute
}

type serverOrderResource[M any, PM serverOrderModelPtr[M]] struct {
	resourceBase
	kind serverOrderKind
}

// NewServerOrderResource orders a server from the standard product range.
func NewServerOrderResource() resource.Resource {
	return &serverOrderResource[serverOrderModel, *serverOrderModel]{kind: serverOrderKind{
		typeSuffix: "_server_order",
		collection: "/order/server/transaction",
		description: "Orders a dedicated server from the standard product range (`hetznerrobot_server_products`). " +
			"Without the `primary_ipv4` addon the server is delivered IPv6-only.",
		productAttr: func() map[string]schema.Attribute {
			return map[string]schema.Attribute{
				"product_id": reqStrNew("Product id, e.g. `EX44` (`hetznerrobot_server_products`). Changing it orders a new server."),
				"location": orderOptStr("Location, e.g. `FSN1`. Required for a product offered in more than one location. " +
					"Changing a set value orders a new server."),
			}
		},
	}}
}

// NewServerMarketOrderResource orders a server from the server market.
func NewServerMarketOrderResource() resource.Resource {
	return &serverOrderResource[serverMarketOrderModel, *serverMarketOrderModel]{kind: serverOrderKind{
		typeSuffix: "_server_market_order",
		collection: "/order/server_market/transaction",
		description: "Orders a dedicated server from the server market (`hetznerrobot_server_market_products`). " +
			"Market offers come and go; a product id that is gone fails the order. " +
			"Without the `primary_ipv4` addon the server is delivered IPv6-only.",
		productAttr: func() map[string]schema.Attribute {
			return map[string]schema.Attribute{
				"product_id": schema.Int64Attribute{
					MarkdownDescription: "Market product id (`hetznerrobot_server_market_products`). Changing it orders a new server.",
					Required:            true,
					PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				},
			}
		},
	}}
}

func (r *serverOrderResource[M, PM]) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + r.kind.typeSuffix
}

func (r *serverOrderResource[M, PM]) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"dist": orderOptStr("Distribution to preinstall, e.g. `Rescue system` or a name from the product's `dists`. " +
			"Changing a set value orders a new server."),
		"lang": orderOptStr("Language of the preinstalled distribution, e.g. `en`."),
		"authorized_keys": orderOptSet("Fingerprints of SSH keys stored in Robot (`hetznerrobot_ssh_key`) to install for root. " +
			"Set this or `password`."),
		"password": func() schema.StringAttribute {
			a := orderOptStr("Root password. Set this or `authorized_keys`; never read back.")
			a.Sensitive = true
			return a
		}(),
		"comment":        orderOptStr("Order comment. **A comment makes Robot process the order manually**, which takes longer."),
		"addons":         orderOptSet("Addon ids, e.g. `primary_ipv4` — without it the server has no IPv4 address."),
		"test":           orderTestAttr(),
		"transaction_id": stableStr("Id of the order transaction, e.g. `B20150121-344958-251479`."),
		"date":           stableStr("Date the order was placed."),
		"status":         computedStr("Transaction status: `in process`, `ready` or `cancelled`."),
		"server_number":  computedInt("Number of the new server, once the transaction is `ready`."),
		"server_ip":      computedStr("Main IPv4 address of the new server, once ready (null for an IPv6-only server)."),
		"host_keys":      computedStrList("Fingerprints of the new server's SSH host keys, once ready."),
	}
	for k, v := range r.kind.productAttr() {
		attrs[k] = v
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: r.kind.description + orderNotes + "\n\nCancel the server with `hetznerrobot_server_cancellation`.",
		Attributes:          attrs,
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true}),
		},
	}
}

func (r *serverOrderResource[M, PM]) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg M
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := PM(&cfg).common()
	if c.AuthorizedKeys.IsNull() && c.Password.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("authorized_keys"), "Missing root access",
			"Robot needs either `authorized_keys` or `password` to order a server.")
	}
}

func (r *serverOrderResource[M, PM]) transactionPath(id string) string {
	return r.kind.collection + "/" + escape(id)
}

func (r *serverOrderResource[M, PM]) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	pm := PM(&plan)
	c := pm.common()
	timeout, diags := c.Timeouts.Create(ctx, orderCreateTimeout)
	resp.Diagnostics.Append(diags...)
	form := url.Values{}
	pm.putProduct(form)
	c.form(ctx, form, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	tx, err := client.SendObject[apiOrderTransaction](ctx, r.client, http.MethodPost, r.kind.collection, "transaction", form)
	if err != nil {
		resp.Diagnostics.AddError("Error ordering server", err.Error())
		return
	}
	c.fillTransaction(tx)
	// From here on the order is placed: the state is saved whatever happens.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if c.Test.ValueBool() {
		return
	}

	final, wait := awaitOrder(ctx, r.client, r.transactionPath(tx.ID), timeout)
	if final.ID != "" {
		c.fillTransaction(final)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	reportOrderWait(&resp.Diagnostics, "server", tx.ID, final.Status, wait)
}

func (r *serverOrderResource[M, PM]) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state M
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	pm := PM(&state)
	c := pm.common()
	tx, err := client.GetObject[apiOrderTransaction](ctx, r.client, r.transactionPath(c.TransactionID.ValueString()), "transaction", nil)
	if client.NotFound(err) {
		return // older than 30 days, or a test order: keep the state as it is
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading server order", err.Error())
		return
	}
	if c.Date.IsNull() { // import: only what the transaction can tell
		pm.importProduct(tx)
	}
	c.fillTransaction(tx)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only takes over the plan: every argument that matters replaces the
// order, so what is left to change in place is the timeouts block and
// arguments set or unset after the fact.
func (r *serverOrderResource[M, PM]) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state M
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Nothing is sent, so what the transaction reported stays — the plan has
	// those computed values as unknown.
	p, st := PM(&plan).common(), PM(&state).common()
	p.TransactionID, p.Date, p.Status = st.TransactionID, st.Date, st.Status
	p.ServerNumber, p.ServerIP, p.HostKeys = st.ServerNumber, st.ServerIP, st.HostKeys
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverOrderResource[M, PM]) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state M
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c := PM(&state).common()
	if c.Test.ValueBool() {
		return
	}
	server := "the ordered server"
	if !c.ServerNumber.IsNull() {
		server = fmt.Sprintf("server %d", c.ServerNumber.ValueInt64())
	}
	resp.Diagnostics.AddWarning("The server was not cancelled",
		fmt.Sprintf("Removing the order only forgets transaction %s; %s keeps running and is billed. "+
			"Cancel it with hetznerrobot_server_cancellation or in Robot.", c.TransactionID.ValueString(), server))
}

// ImportState insists on a readable transaction: a state holding nothing but the
// id would plan product_id from null to the configured value — a new order.
func (r *serverOrderResource[M, PM]) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := client.GetObject[apiOrderTransaction](ctx, r.client, r.transactionPath(req.ID), "transaction", nil); err != nil {
		resp.Diagnostics.AddError("Cannot import server order",
			fmt.Sprintf("Transaction %s cannot be read (Robot keeps transactions for 30 days): %v", req.ID, err))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("transaction_id"), req, resp)
}

// orderPollInterval spaces the transaction reads of a waiting order: an order
// can take hours, and the transaction endpoints allow 500 requests per hour —
// a minute apart leaves room for several orders and everything else.
func orderPollInterval() time.Duration { return 6 * pollInterval }

// awaitOrder polls a transaction until it leaves "in process" or timeout
// passes. It returns the last transaction it read and the reason the wait
// ended early (nil when the transaction reached an end state).
func awaitOrder(ctx context.Context, c *client.Client, txPath string, timeout time.Duration) (apiOrderTransaction, error) {
	var last apiOrderTransaction
	err := waitEvery(ctx, timeout, orderPollInterval(), "the order transaction to become ready", func() (bool, error) {
		tx, err := client.GetObject[apiOrderTransaction](ctx, c, txPath, "transaction", nil)
		if err != nil {
			return false, err
		}
		last = tx
		return tx.Status == orderStatusReady || tx.Status == orderStatusCancelled, nil
	})
	return last, err
}

// reportOrderWait turns the end of a wait into diagnostics: a cancelled order
// is an error, a wait that ended early only a warning — the order is placed and
// must not be placed again.
func reportOrderWait(diags *diag.Diagnostics, what, txID, status string, wait error) {
	switch {
	case status == orderStatusCancelled:
		// A warning, not an error: an error would taint the resource, and the next
		// apply would replace it — that is, order again.
		diags.AddWarning("Order cancelled",
			fmt.Sprintf("Robot cancelled the %s order (transaction %s); status stays `cancelled`. Check the order in Robot "+
				"and replace the resource deliberately (tofu apply -replace=…) to order again.", what, txID))
	case wait != nil:
		diags.AddWarning("Order placed, still in process",
			fmt.Sprintf("The %s order (transaction %s) was placed, but it is not ready yet: %v. "+
				"Nothing will be ordered again — run a refresh or apply later and the missing details are filled in.", what, txID, wait))
	}
}

// --- addon order ---

// NewServerAddonOrderResource orders an addon (an IP, a subnet) for a server.
func NewServerAddonOrderResource() resource.Resource { return &serverAddonOrderResource{} }

type serverAddonOrderResource struct{ resourceBase }

type apiAddonTransaction struct {
	ID           string            `json:"id"`
	Date         client.FlexString `json:"date"`
	Status       string            `json:"status"`
	ServerNumber client.FlexInt    `json:"server_number"`
	Product      struct {
		ID   string            `json:"id"`
		Name client.FlexString `json:"name"`
		Type client.FlexString `json:"type"`
	} `json:"product"`
	Resources []struct {
		Type client.FlexString `json:"type"`
		ID   client.FlexString `json:"id"`
	} `json:"resources"`
}

type addonOrderResourceModel struct {
	Type types.String `tfsdk:"type"`
	ID   types.String `tfsdk:"id"`
}

var addonOrderResourceType = types.ObjectType{AttrTypes: map[string]attr.Type{"type": types.StringType, "id": types.StringType}}

type serverAddonOrderModel struct {
	ServerNumber  types.Int64    `tfsdk:"server_number"`
	ProductID     types.String   `tfsdk:"product_id"`
	Reason        types.String   `tfsdk:"reason"`
	Gateway       types.String   `tfsdk:"gateway"`
	Test          types.Bool     `tfsdk:"test"`
	TransactionID types.String   `tfsdk:"transaction_id"`
	Date          types.String   `tfsdk:"date"`
	Status        types.String   `tfsdk:"status"`
	ProductName   types.String   `tfsdk:"product_name"`
	ProductType   types.String   `tfsdk:"product_type"`
	Resources     types.List     `tfsdk:"resources"`
	Timeouts      timeouts.Value `tfsdk:"timeouts"`
}

func (m *serverAddonOrderModel) fill(ctx context.Context, tx apiAddonTransaction, diags *diag.Diagnostics) {
	m.TransactionID = types.StringValue(tx.ID)
	m.Date = flexStr(tx.Date)
	m.Status = types.StringValue(tx.Status)
	m.ProductName = flexStr(tx.Product.Name)
	// The transaction read may leave out the type the order answer carried.
	if tx.Product.Type.Valid || m.ProductType.IsNull() || m.ProductType.IsUnknown() {
		m.ProductType = flexStr(tx.Product.Type)
	}
	res := make([]addonOrderResourceModel, len(tx.Resources))
	for i, r := range tx.Resources {
		res[i] = addonOrderResourceModel{Type: flexStr(r.Type), ID: flexStr(r.ID)}
	}
	list, d := types.ListValueFrom(ctx, addonOrderResourceType, res)
	diags.Append(d...)
	m.Resources = list
}

func (r *serverAddonOrderResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_addon_order"
}

func (r *serverAddonOrderResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Orders an addon for a server — an additional IPv4 address, a subnet or a failover subnet " +
			"(`hetznerrobot_server_addon_products`). The new IP or subnet appears in `resources` once the order is ready." + orderNotes +
			"\n\nCancel an IP with `hetznerrobot_ip_cancellation`, a subnet with `hetznerrobot_subnet_cancellation`.",
		Attributes: map[string]schema.Attribute{
			"server_number": reqIntNew("Server the addon is ordered for."),
			"product_id":    reqStrNew("Addon product id, e.g. `additional_ipv4`, `subnet_ipv4_29` or `failover_subnet_ipv4_29`."),
			"reason": orderOptStr("RIPE reason — why the addresses are needed. Robot requires it for the product types " +
				"`ip_ipv4`, `subnet_ipv4` and `failover_subnet_ipv4`."),
			"gateway": orderOptStr("Routing target of a `subnet_ipv4` addon: one of the server's additional IPv4 addresses. " +
				"Defaults to the server's main IP."),
			"test":           orderTestAttr(),
			"transaction_id": stableStr("Id of the order transaction."),
			"date":           stableStr("Date the order was placed."),
			"status":         computedStr("Transaction status: `in process`, `ready` or `cancelled`."),
			"product_name":   computedStr("Name of the ordered addon."),
			"product_type":   computedStr("Type of the ordered addon, e.g. `ip_ipv4`."),
			"resources": schema.ListNestedAttribute{
				MarkdownDescription: "What the order created, once ready — e.g. `{type = \"ip\", id = \"203.0.113.7\"}`.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"type": computedStr("Type of the resource, e.g. `ip` or `subnet`."),
					"id":   computedStr("Id of the resource — the IP address or the subnet's network address."),
				}},
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{Create: true}),
		},
	}
}

func addonTransactionPath(id string) string { return "/order/server_addon/transaction/" + escape(id) }

func (r *serverAddonOrderResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serverAddonOrderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, orderCreateTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	form := url.Values{"product_id": {plan.ProductID.ValueString()}}
	formInt(form, "server_number", plan.ServerNumber)
	formStr(form, "reason", plan.Reason)
	formStr(form, "gateway", plan.Gateway)
	if plan.Test.ValueBool() {
		form.Set("test", "true")
	}

	tx, err := client.SendObject[apiAddonTransaction](ctx, r.client, http.MethodPost, "/order/server_addon/transaction", "transaction", form)
	if err != nil {
		resp.Diagnostics.AddError("Error ordering server addon", err.Error())
		return
	}
	plan.fill(ctx, tx, &resp.Diagnostics)
	// From here on the order is placed: the state is saved whatever happens.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if plan.Test.ValueBool() {
		return
	}

	var last apiAddonTransaction
	wait := waitEvery(ctx, timeout, orderPollInterval(), "the addon order to become ready", func() (bool, error) {
		got, err := client.GetObject[apiAddonTransaction](ctx, r.client, addonTransactionPath(tx.ID), "transaction", nil)
		if err != nil {
			return false, err
		}
		last = got
		return got.Status == orderStatusReady || got.Status == orderStatusCancelled, nil
	})
	if last.ID != "" {
		plan.fill(ctx, last, &resp.Diagnostics)
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	}
	reportOrderWait(&resp.Diagnostics, "addon", tx.ID, last.Status, wait)
}

func (r *serverAddonOrderResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverAddonOrderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tx, err := client.GetObject[apiAddonTransaction](ctx, r.client, addonTransactionPath(state.TransactionID.ValueString()), "transaction", nil)
	if client.NotFound(err) {
		return // older than 30 days, or a test order: keep the state as it is
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading server addon order", err.Error())
		return
	}
	if state.Date.IsNull() { // import
		state.ProductID = types.StringValue(tx.Product.ID)
		state.ServerNumber = flexInt(tx.ServerNumber)
	}
	state.fill(ctx, tx, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only takes over the plan — the timeouts block is all that changes in
// place.
func (r *serverAddonOrderResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serverAddonOrderModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.TransactionID, plan.Date, plan.Status = state.TransactionID, state.Date, state.Status
	plan.ProductName, plan.ProductType, plan.Resources = state.ProductName, state.ProductType, state.Resources
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverAddonOrderResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverAddonOrderModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.Test.ValueBool() {
		return
	}
	resp.Diagnostics.AddWarning("The addon was not cancelled",
		fmt.Sprintf("Removing the order only forgets transaction %s; the ordered addon stays and is billed. "+
			"Cancel it with hetznerrobot_ip_cancellation / hetznerrobot_subnet_cancellation or in Robot.", state.TransactionID.ValueString()))
}

// ImportState insists on a readable transaction, as the server orders do.
func (r *serverAddonOrderResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, err := client.GetObject[apiAddonTransaction](ctx, r.client, addonTransactionPath(req.ID), "transaction", nil); err != nil {
		resp.Diagnostics.AddError("Cannot import server addon order",
			fmt.Sprintf("Transaction %s cannot be read (Robot keeps transactions for 30 days): %v", req.ID, err))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("transaction_id"), req, resp)
}
