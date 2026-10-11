package provider

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

// configureClient lets the tests shrink the client's pacing; the provider uses
// the client as built.
var configureClient = func(*client.Client) {}

// pollInterval spaces the reads that wait for an asynchronous change (firewall
// and vSwitch updates, orders). The tests shrink it.
var pollInterval = 10 * time.Second

// resourceBase gives a resource the configured API client.
type resourceBase struct {
	client *client.Client
}

func (b *resourceBase) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	b.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

// dataSourceBase gives a data source the configured API client.
type dataSourceBase struct {
	client *client.Client
}

func (b *dataSourceBase) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	b.client = clientFrom(req.ProviderData, &resp.Diagnostics)
}

func clientFrom(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil // provider not configured yet (validate/plan before Configure)
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("expected *client.Client, got %T", data))
		return nil
	}
	return c
}

// --- schema constructors ---

// optStr/optInt/optBool are settings the practitioner may leave out: Optional +
// Computed + UseStateForUnknown, so a left-out setting keeps the value Robot
// holds and never plans as (known after apply).
func optStr(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func optInt(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: desc,
		Optional:            true,
		Computed:            true,
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
	}
}

func optBool(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		Computed:            true,
		PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
	}
}

// reqStr is a required setting that can change in place.
func reqStr(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Required: true}
}

// The *New constructors build arguments whose change replaces the resource.

func reqStrNew(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Required:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
	}
}

func reqIntNew(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: desc,
		Required:            true,
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
	}
}

// replaceOnSetChange replaces only when a value that was set changes to
// another set value. Setting a value the state does not know (after an import
// that cannot read it back) or unsetting one changes nothing at Robot, so it
// must not destroy and recreate what exists — a withdrawn cancellation or a
// fresh boot activation with a new password.
type replaceOnSetChange struct{}

func (replaceOnSetChange) Description(context.Context) string {
	return "Replaces the resource when a set value changes to another set value."
}

func (m replaceOnSetChange) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (replaceOnSetChange) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	resp.RequiresReplace = !req.PlanValue.Equal(req.StateValue)
}

func (replaceOnSetChange) PlanModifyBool(_ context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	if req.State.Raw.IsNull() || req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	resp.RequiresReplace = !req.PlanValue.Equal(req.StateValue)
}

func (replaceOnSetChange) PlanModifySet(_ context.Context, req planmodifier.SetRequest, resp *planmodifier.SetResponse) {
	if req.State.Raw.IsNull() || req.StateValue.IsNull() || req.PlanValue.IsNull() || req.PlanValue.IsUnknown() {
		return
	}
	resp.RequiresReplace = !req.PlanValue.Equal(req.StateValue)
}

// triggersAttr re-runs a one-shot action (reset, Wake on LAN) whenever one of
// its values changes, like the triggers of terraform_data.
func triggersAttr() schema.MapAttribute {
	return schema.MapAttribute{
		MarkdownDescription: "Arbitrary values; changing any of them runs the action again (the resource is replaced).",
		Optional:            true,
		ElementType:         types.StringType,
		PlanModifiers:       []planmodifier.Map{mapplanmodifier.RequiresReplace()},
	}
}

// computedStr/Int/Bool are read-only values that may change between applies.
// They carry no UseStateForUnknown: a value Robot changes on its own (a status,
// a paid-until date) must be allowed to differ after an update.
func computedStr(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
}

func computedInt(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: desc, Computed: true}
}

func computedBool(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: desc, Computed: true}
}

func computedStrList(desc string) schema.ListAttribute {
	return schema.ListAttribute{MarkdownDescription: desc, Computed: true, ElementType: types.StringType}
}

// stableStr/Int are read-only values fixed for the entity's lifetime (an
// address, a product, a fingerprint), so they keep their state across updates.
func stableStr(desc string) schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

func stableInt(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: desc,
		Computed:            true,
		PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
	}
}

func stableStrList(desc string) schema.ListAttribute {
	return schema.ListAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		ElementType:         types.StringType,
		PlanModifiers:       []planmodifier.List{listplanmodifier.UseStateForUnknown()},
	}
}

func sensitiveStable(desc string) schema.StringAttribute {
	a := stableStr(desc)
	a.Sensitive = true
	return a
}

// --- value conversions ---

func flexStr(f client.FlexString) types.String {
	if !f.Valid {
		return types.StringNull()
	}
	return types.StringValue(f.Value)
}

func flexInt(f client.FlexInt) types.Int64 {
	if !f.Valid {
		return types.Int64Null()
	}
	return types.Int64Value(f.Value)
}

func flexFloat(f client.FlexFloat) types.Float64 {
	if !f.Valid {
		return types.Float64Null()
	}
	return types.Float64Value(f.Value)
}

// strPtr maps a JSON string that may be null.
func strPtr(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// stringList converts a Go slice into a framework list (never null).
func stringList(v []string) types.List {
	elems := make([]attr.Value, len(v))
	for i, s := range v {
		elems[i] = types.StringValue(s)
	}
	return types.ListValueMust(types.StringType, elems)
}

// stringSet converts a Go slice into a framework set (never null).
func stringSet(v []string) types.Set {
	elems := make([]attr.Value, len(v))
	for i, s := range v {
		elems[i] = types.StringValue(s)
	}
	return types.SetValueMust(types.StringType, elems)
}

// strings reads a list or set of strings; null and unknown read as nil.
func valueStrings(ctx context.Context, v interface {
	IsNull() bool
	IsUnknown() bool
	ElementsAs(context.Context, any, bool) diag.Diagnostics
}, diags *diag.Diagnostics) []string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	var out []string
	diags.Append(v.ElementsAs(ctx, &out, false)...)
	return out
}

// --- form bodies ---

// formStr/formInt/formBool add a key when the value is known and not null.
func formStr(f url.Values, key string, v types.String) {
	if !v.IsNull() && !v.IsUnknown() {
		f.Set(key, v.ValueString())
	}
}

func formInt(f url.Values, key string, v types.Int64) {
	if !v.IsNull() && !v.IsUnknown() {
		f.Set(key, strconv.FormatInt(v.ValueInt64(), 10))
	}
}

func formBool(f url.Values, key string, v types.Bool) {
	if !v.IsNull() && !v.IsUnknown() {
		f.Set(key, client.Bool(v.ValueBool()))
	}
}

// formArray adds key[]=v for every value.
func formArray(f url.Values, key string, values []string) {
	for _, v := range values {
		f.Add(key+"[]", v)
	}
}

// --- ids & paths ---

// splitImportID splits an import id of the form "a/b" into exactly len(names)
// non-empty parts. Only the first separator count matters: a part may not
// contain "/" itself, except the last.
func splitImportID(id string, names ...string) ([]string, error) {
	parts := strings.SplitN(id, "/", len(names))
	if len(parts) != len(names) {
		return nil, fmt.Errorf("expected import id %q, got %q", strings.Join(names, "/"), id)
	}
	for i, p := range parts {
		if strings.TrimSpace(p) == "" {
			return nil, fmt.Errorf("import id part %s is empty in %q", names[i], id)
		}
	}
	return parts, nil
}

func parseInt64(s, name string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s %q is not a positive integer", name, s)
	}
	return n, nil
}

// escape makes an IP, a subnet or a fingerprint safe as a path segment. The
// API takes them verbatim (colons included), so only what would break the URL
// is escaped.
func escape(s string) string { return url.PathEscape(s) }

// --- waiting ---

// waitFor calls check every pollInterval until it reports done, it fails, or
// timeout passes. The error on timeout names what was awaited.
func waitFor(ctx context.Context, timeout time.Duration, what string, check func() (bool, error)) error {
	return waitEvery(ctx, timeout, pollInterval, what, check)
}

// waitEvery is waitFor with its own interval — for waits that last long enough
// to matter against an hourly request limit.
func waitEvery(ctx context.Context, timeout, interval time.Duration, what string, check func() (bool, error)) error {
	deadline := time.Now().Add(timeout)
	for {
		done, err := check()
		if err != nil || done {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for %s", timeout, what)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
