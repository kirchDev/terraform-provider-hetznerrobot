package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

// firewallTimeout bounds the wait for a firewall change, which Robot applies
// asynchronously ("in process") and usually within a minute.
const firewallTimeout = 15 * time.Minute

// --- rules, shared by the firewall and its templates ---

type apiFirewallRule struct {
	IPVersion client.FlexString `json:"ip_version"`
	Name      client.FlexString `json:"name"`
	DstIP     client.FlexString `json:"dst_ip"`
	SrcIP     client.FlexString `json:"src_ip"`
	DstPort   client.FlexString `json:"dst_port"`
	SrcPort   client.FlexString `json:"src_port"`
	Protocol  client.FlexString `json:"protocol"`
	TCPFlags  client.FlexString `json:"tcp_flags"`
	Action    string            `json:"action"`
}

// apiFirewallRules is the "rules" object. A firewall without rules answers
// {} — or [], the way an empty PHP array encodes — so both decode as empty.
type apiFirewallRules struct {
	Input  []apiFirewallRule `json:"input"`
	Output []apiFirewallRule `json:"output"`
}

func (r *apiFirewallRules) UnmarshalJSON(b []byte) error {
	*r = apiFirewallRules{}
	t := bytes.TrimSpace(b)
	if len(t) == 0 || t[0] != '{' {
		return nil
	}
	type plain apiFirewallRules
	return json.Unmarshal(t, (*plain)(r))
}

type firewallRuleModel struct {
	Name      types.String `tfsdk:"name"`
	IPVersion types.String `tfsdk:"ip_version"`
	DstIP     types.String `tfsdk:"dst_ip"`
	SrcIP     types.String `tfsdk:"src_ip"`
	DstPort   types.String `tfsdk:"dst_port"`
	SrcPort   types.String `tfsdk:"src_port"`
	Protocol  types.String `tfsdk:"protocol"`
	TCPFlags  types.String `tfsdk:"tcp_flags"`
	Action    types.String `tfsdk:"action"`
}

var firewallRuleType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"name": types.StringType, "ip_version": types.StringType, "dst_ip": types.StringType,
	"src_ip": types.StringType, "dst_port": types.StringType, "src_port": types.StringType,
	"protocol": types.StringType, "tcp_flags": types.StringType, "action": types.StringType,
}}

// fields pairs every form key of a rule with its value.
func (m firewallRuleModel) fields() [][2]any {
	return [][2]any{
		{"name", m.Name}, {"ip_version", m.IPVersion}, {"dst_ip", m.DstIP}, {"src_ip", m.SrcIP},
		{"dst_port", m.DstPort}, {"src_port", m.SrcPort}, {"protocol", m.Protocol},
		{"tcp_flags", m.TCPFlags}, {"action", m.Action},
	}
}

func firewallRulesList(rules []apiFirewallRule) types.List {
	models := make([]attr.Value, len(rules))
	for i, r := range rules {
		models[i] = types.ObjectValueMust(firewallRuleType.AttrTypes, map[string]attr.Value{
			"name": flexStr(r.Name), "ip_version": flexStr(r.IPVersion), "dst_ip": flexStr(r.DstIP),
			"src_ip": flexStr(r.SrcIP), "dst_port": flexStr(r.DstPort), "src_port": flexStr(r.SrcPort),
			"protocol": flexStr(r.Protocol), "tcp_flags": flexStr(r.TCPFlags), "action": types.StringValue(r.Action),
		})
	}
	return types.ListValueMust(firewallRuleType, models)
}

func firewallRuleModels(ctx context.Context, l types.List, diags *diag.Diagnostics) []firewallRuleModel {
	if l.IsNull() || l.IsUnknown() {
		return nil
	}
	var out []firewallRuleModel
	diags.Append(l.ElementsAs(ctx, &out, false)...)
	return out
}

// formFirewallRules encodes one direction as rules[<dir>][<i>][<field>]; a
// null field is left out, which Robot treats as a wildcard. Every write sends
// the complete rule set — a direction without rules sends nothing and ends up
// empty.
func formFirewallRules(f url.Values, dir string, rules []firewallRuleModel) {
	for i, r := range rules {
		for _, kv := range r.fields() {
			v := kv[1].(types.String)
			if v.IsNull() || v.IsUnknown() {
				continue
			}
			f.Set(fmt.Sprintf("rules[%s][%d][%s]", dir, i, kv[0]), v.ValueString())
		}
	}
}

func firewallRuleSchema(desc string, computed bool, modifiers []planmodifier.List) schema.ListNestedAttribute {
	opt := func(d string, v ...validator.String) schema.StringAttribute {
		return schema.StringAttribute{
			MarkdownDescription: d,
			Optional:            true,
			Validators:          append([]validator.String{stringvalidator.LengthAtLeast(1)}, v...),
		}
	}
	return schema.ListNestedAttribute{
		MarkdownDescription: desc,
		Optional:            true,
		Computed:            computed,
		PlanModifiers:       modifiers,
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"name":       opt("Name of the rule."),
			"ip_version": opt("`ipv4` or `ipv6`; left out, the rule matches both.", stringvalidator.OneOf("ipv4", "ipv6")),
			"dst_ip":     opt("Destination address or subnet in CIDR notation (IPv4 rules only)."),
			"src_ip":     opt("Source address or subnet in CIDR notation (IPv4 rules only)."),
			"dst_port":   opt("Destination port or range, e.g. `80` or `32768-65535`."),
			"src_port":   opt("Source port or range."),
			"protocol": opt("`tcp`, `udp`, `gre`, `icmp`, `ipip`, `ah` or `esp`; needs `ip_version`.",
				stringvalidator.OneOf("tcp", "udp", "gre", "icmp", "ipip", "ah", "esp")),
			"tcp_flags": opt("TCP flag or logical combination of flags, e.g. `syn` or `syn|ack`."),
			"action": schema.StringAttribute{
				MarkdownDescription: "`accept` or `discard`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("accept", "discard")},
			},
		}},
	}
}

func dsFirewallRuleAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"name":       dsStr("Name of the rule."),
		"ip_version": dsStr("`ipv4`, `ipv6`, or null for both."),
		"dst_ip":     dsStr("Destination address or subnet."),
		"src_ip":     dsStr("Source address or subnet."),
		"dst_port":   dsStr("Destination port or range."),
		"src_port":   dsStr("Source port or range."),
		"protocol":   dsStr("Protocol above the IP layer."),
		"tcp_flags":  dsStr("TCP flags."),
		"action":     dsStr("`accept` or `discard`."),
	}
}

// validateFirewallRules enforces the documented IPv6 limitations at plan time:
// addresses can only be filtered in IPv4 rules, and a protocol needs an IP
// version.
func validateFirewallRules(ctx context.Context, attrName string, l types.List, diags *diag.Diagnostics) {
	for i, r := range firewallRuleModels(ctx, l, diags) {
		p := path.Root(attrName).AtListIndex(i)
		ipv4 := !r.IPVersion.IsUnknown() && r.IPVersion.ValueString() == "ipv4"
		for name, v := range map[string]types.String{"src_ip": r.SrcIP, "dst_ip": r.DstIP} {
			if !v.IsNull() && !v.IsUnknown() && !ipv4 && !r.IPVersion.IsUnknown() {
				diags.AddAttributeError(p.AtName(name), "Address filter needs IPv4",
					"Robot can filter on source and destination addresses only in rules with ip_version = \"ipv4\".")
			}
		}
		if !r.Protocol.IsNull() && !r.Protocol.IsUnknown() && r.IPVersion.IsNull() {
			diags.AddAttributeError(p.AtName("protocol"), "Protocol filter needs an IP version",
				"Robot cannot filter on a protocol in a rule without ip_version.")
		}
	}
}

// --- the firewall of a server ---

var (
	_ resource.Resource                   = (*firewallResource)(nil)
	_ resource.ResourceWithImportState    = (*firewallResource)(nil)
	_ resource.ResourceWithValidateConfig = (*firewallResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*firewallResource)(nil)
)

// NewFirewallResource manages the whole firewall configuration of a server.
func NewFirewallResource() resource.Resource { return &firewallResource{} }

type firewallResource struct{ resourceBase }

type apiFirewall struct {
	ServerIP     string           `json:"server_ip"`
	ServerNumber client.FlexInt   `json:"server_number"`
	Status       string           `json:"status"`
	FilterIPv6   bool             `json:"filter_ipv6"`
	WhitelistHOS bool             `json:"whitelist_hos"`
	Port         string           `json:"port"`
	Rules        apiFirewallRules `json:"rules"`
}

type firewallModel struct {
	ServerNumber types.Int64  `tfsdk:"server_number"`
	ServerIP     types.String `tfsdk:"server_ip"`
	Port         types.String `tfsdk:"port"`
	Status       types.String `tfsdk:"status"`
	FilterIPv6   types.Bool   `tfsdk:"filter_ipv6"`
	WhitelistHOS types.Bool   `tfsdk:"whitelist_hos"`
	TemplateID   types.Int64  `tfsdk:"template_id"`
	InputRules   types.List   `tfsdk:"input_rules"`
	OutputRules  types.List   `tfsdk:"output_rules"`
}

func (m *firewallModel) fill(f apiFirewall) {
	m.ServerIP = types.StringValue(f.ServerIP)
	m.Port = types.StringValue(f.Port)
	// "in process" is transient (an out-of-band change being applied); storing
	// it would plan status = "in process", which Robot refuses as input.
	if f.Status != "in process" || m.Status.IsNull() || m.Status.IsUnknown() {
		m.Status = types.StringValue(f.Status)
	}
	m.FilterIPv6 = types.BoolValue(f.FilterIPv6)
	m.WhitelistHOS = types.BoolValue(f.WhitelistHOS)
	m.InputRules = firewallRulesList(f.Rules.Input)
	m.OutputRules = firewallRulesList(f.Rules.Output)
}

func firewallPath(serverNumber int64) string {
	return "/firewall/" + strconv.FormatInt(serverNumber, 10)
}

func (r *firewallResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall"
}

func (r *firewallResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepRules := []planmodifier.List{listplanmodifier.UseStateForUnknown()}
	conflicts := int64validator.ConflictsWith(path.MatchRoot("input_rules"), path.MatchRoot("output_rules"), path.MatchRoot("whitelist_hos"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "The complete Robot firewall of one dedicated server — status, flags and every input and output rule. " +
			"Each apply sends the whole configuration and waits until Robot has applied it. " +
			"Either configure the rules here or apply a `template_id`; whatever is left out keeps the value Robot holds — " +
			"adopting a firewall without listing its rules keeps them. " +
			"Destroying the resource clears the firewall configuration.",
		Attributes: map[string]schema.Attribute{
			"server_number": reqIntNew("Server the firewall belongs to."),
			"server_ip":     stableStr("Main IP address of the server."),
			"port":          stableStr("Switch port the firewall sits on (`main` or `kvm`)."),
			"status": func() schema.StringAttribute {
				a := optStr("`active` or `disabled`. Left out, it becomes `active` when rules or a template are configured and keeps the live status otherwise.")
				a.Validators = []validator.String{stringvalidator.OneOf("active", "disabled")}
				return a
			}(),
			"filter_ipv6":   optBool("Whether IPv6 traffic is filtered too."),
			"whitelist_hos": optBool("Whether Hetzner services (rescue, DHCP, …) are always let through. Left out, it keeps the live value."),
			"template_id": schema.Int64Attribute{
				MarkdownDescription: "Firewall template to apply. It is applied when it is set or changed — later edits of the template " +
					"do not reach this server until the template is applied again. Conflicts with the rules and `whitelist_hos`.",
				Optional:   true,
				Validators: []validator.Int64{conflicts},
			},
			"input_rules":  firewallRuleSchema("Rules for incoming packets, matched in order.", true, keepRules),
			"output_rules": firewallRuleSchema("Rules for outgoing packets, matched in order.", true, keepRules),
		},
	}
}

func (r *firewallResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg firewallModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateFirewallRules(ctx, "input_rules", cfg.InputRules, &resp.Diagnostics)
	validateFirewallRules(ctx, "output_rules", cfg.OutputRules, &resp.Diagnostics)
}

// ModifyPlan marks what a template brings along — the flags and the rules —
// as unknown when a template is (re)applied. They keep their state otherwise
// (UseStateForUnknown), and the template's values would then not match the plan.
func (r *firewallResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, cfg firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || plan.TemplateID.IsNull() {
		return
	}
	prior := types.Int64Null()
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("template_id"), &prior)...)
	}
	if plan.TemplateID.Equal(prior) {
		return
	}
	if cfg.WhitelistHOS.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("whitelist_hos"), types.BoolUnknown())...)
	}
	if cfg.FilterIPv6.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("filter_ipv6"), types.BoolUnknown())...)
	}
	if cfg.InputRules.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("input_rules"), types.ListUnknown(firewallRuleType))...)
	}
	if cfg.OutputRules.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("output_rules"), types.ListUnknown(firewallRuleType))...)
	}
}

// firewallSend issues a firewall write, waiting out FIREWALL_IN_PROCESS (an
// earlier change still being applied), then waits until Robot has applied it.
func firewallSend(ctx context.Context, c *client.Client, method string, serverNumber int64, form url.Values) (apiFirewall, error) {
	p := firewallPath(serverNumber)
	err := waitFor(ctx, firewallTimeout, "the previous firewall change to finish", func() (bool, error) {
		err := c.Send(ctx, method, p, form, nil)
		if client.HasCode(err, "FIREWALL_IN_PROCESS") {
			return false, nil
		}
		return true, err
	})
	if err != nil {
		return apiFirewall{}, err
	}
	var got apiFirewall
	err = waitFor(ctx, firewallTimeout, "Robot to apply the firewall", func() (bool, error) {
		var err error
		got, err = client.GetObject[apiFirewall](ctx, c, p, "firewall", nil)
		return err == nil && got.Status != "in process", err
	})
	return got, err
}

// configuresRules reports whether the plan sets rules or a template itself.
func configuresRules(plan *firewallModel) bool {
	known := func(l types.List) bool { return !l.IsNull() && !l.IsUnknown() }
	return known(plan.InputRules) || known(plan.OutputRules) || !plan.TemplateID.IsNull()
}

// write builds the complete configuration from the plan. Values the plan does
// not know (left out of the configuration while something else changed) come
// from prior, so a status change never drops the rules.
func (r *firewallResource) write(ctx context.Context, plan, prior *firewallModel, diags *diag.Diagnostics) {
	form := url.Values{}
	status := "active"
	switch {
	case !plan.Status.IsNull() && !plan.Status.IsUnknown():
		status = plan.Status.ValueString()
	case configuresRules(plan):
		// Rules or a template without a status: they are meant to filter.
	case prior != nil && !prior.Status.IsNull() && prior.Status.ValueString() != "in process":
		status = prior.Status.ValueString()
	}
	form.Set("status", status)
	formBool(form, "filter_ipv6", plan.FilterIPv6)

	applyTemplate := !plan.TemplateID.IsNull() && (prior == nil || !plan.TemplateID.Equal(prior.TemplateID))
	if applyTemplate {
		formInt(form, "template_id", plan.TemplateID)
	} else {
		pick := func(planned, before types.List) []firewallRuleModel {
			if planned.IsUnknown() && prior != nil {
				return firewallRuleModels(ctx, before, diags)
			}
			return firewallRuleModels(ctx, planned, diags)
		}
		var priorIn, priorOut types.List
		whitelist := types.BoolValue(true)
		if prior != nil {
			priorIn, priorOut, whitelist = prior.InputRules, prior.OutputRules, prior.WhitelistHOS
		}
		if !plan.WhitelistHOS.IsUnknown() {
			whitelist = plan.WhitelistHOS
		}
		formBool(form, "whitelist_hos", whitelist)
		formFirewallRules(form, "input", pick(plan.InputRules, priorIn))
		formFirewallRules(form, "output", pick(plan.OutputRules, priorOut))
	}
	if diags.HasError() {
		return
	}
	got, err := firewallSend(ctx, r.client, http.MethodPost, plan.ServerNumber.ValueInt64(), form)
	if err != nil {
		diags.AddError("Error applying firewall configuration", err.Error())
		return
	}
	plan.fill(got)
}

func (r *firewallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Every POST replaces the whole configuration. Adopting a firewall without
	// listing its rules must keep them, so the live configuration is the prior
	// the plan's unknowns fall back to — never an empty rule set.
	current, err := client.GetObject[apiFirewall](ctx, r.client, firewallPath(plan.ServerNumber.ValueInt64()), "firewall", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall", err.Error())
		return
	}
	var prior firewallModel
	prior.fill(current)
	prior.TemplateID = types.Int64Null()
	r.write(ctx, &plan, &prior, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *firewallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiFirewall](ctx, r.client, firewallPath(state.ServerNumber.ValueInt64()), "firewall", nil)
	if client.NotFound(err) { // SERVER_NOT_FOUND, FIREWALL_NOT_AVAILABLE
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall", err.Error())
		return
	}
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *firewallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state firewallModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, &plan, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *firewallResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state firewallModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := firewallSend(ctx, r.client, http.MethodDelete, state.ServerNumber.ValueInt64(), nil)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error clearing firewall configuration", err.Error())
	}
}

func (r *firewallResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := parseInt64(req.ID, "server_number")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_number"), n)...)
}

// --- data source ---

// NewFirewallDataSource reads the firewall configuration of a server.
func NewFirewallDataSource() datasource.DataSource { return &firewallDataSource{} }

type firewallDataSource struct{ dataSourceBase }

type firewallDSModel struct {
	ServerNumber types.Int64  `tfsdk:"server_number"`
	ServerIP     types.String `tfsdk:"server_ip"`
	Port         types.String `tfsdk:"port"`
	Status       types.String `tfsdk:"status"`
	FilterIPv6   types.Bool   `tfsdk:"filter_ipv6"`
	WhitelistHOS types.Bool   `tfsdk:"whitelist_hos"`
	InputRules   types.List   `tfsdk:"input_rules"`
	OutputRules  types.List   `tfsdk:"output_rules"`
}

func (d *firewallDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall"
}

func (d *firewallDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads the Robot firewall configuration of one dedicated server.",
		Attributes: map[string]dschema.Attribute{
			"server_number": dsRequiredInt("Server the firewall belongs to."),
			"server_ip":     dsStr("Main IP address of the server."),
			"port":          dsStr("Switch port the firewall sits on (`main` or `kvm`)."),
			"status":        dsStr("`active`, `disabled` or `in process`."),
			"filter_ipv6":   dsBool("Whether IPv6 traffic is filtered too."),
			"whitelist_hos": dsBool("Whether Hetzner services are always let through."),
			"input_rules":   dsList("Rules for incoming packets.", dsFirewallRuleAttrs()),
			"output_rules":  dsList("Rules for outgoing packets.", dsFirewallRuleAttrs()),
		},
	}
}

func (d *firewallDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg firewallDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiFirewall](ctx, d.client, firewallPath(cfg.ServerNumber.ValueInt64()), "firewall", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall", err.Error())
		return
	}
	var m firewallModel
	m.fill(got)
	cfg.ServerIP, cfg.Port, cfg.Status = m.ServerIP, m.Port, m.Status
	cfg.FilterIPv6, cfg.WhitelistHOS = m.FilterIPv6, m.WhitelistHOS
	cfg.InputRules, cfg.OutputRules = m.InputRules, m.OutputRules
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
