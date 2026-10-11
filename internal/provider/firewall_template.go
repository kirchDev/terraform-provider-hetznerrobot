package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

var (
	_ resource.Resource                   = (*firewallTemplateResource)(nil)
	_ resource.ResourceWithImportState    = (*firewallTemplateResource)(nil)
	_ resource.ResourceWithValidateConfig = (*firewallTemplateResource)(nil)
)

// NewFirewallTemplateResource manages a reusable firewall template.
func NewFirewallTemplateResource() resource.Resource { return &firewallTemplateResource{} }

type firewallTemplateResource struct{ resourceBase }

type apiFirewallTemplate struct {
	ID           int64            `json:"id"`
	Name         string           `json:"name"`
	FilterIPv6   bool             `json:"filter_ipv6"`
	WhitelistHOS bool             `json:"whitelist_hos"`
	IsDefault    bool             `json:"is_default"`
	Rules        apiFirewallRules `json:"rules"`
}

type firewallTemplateModel struct {
	ID           types.Int64  `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	FilterIPv6   types.Bool   `tfsdk:"filter_ipv6"`
	WhitelistHOS types.Bool   `tfsdk:"whitelist_hos"`
	IsDefault    types.Bool   `tfsdk:"is_default"`
	InputRules   types.List   `tfsdk:"input_rules"`
	OutputRules  types.List   `tfsdk:"output_rules"`
}

func (m *firewallTemplateModel) fill(t apiFirewallTemplate) {
	m.ID = types.Int64Value(t.ID)
	m.Name = types.StringValue(t.Name)
	m.FilterIPv6 = types.BoolValue(t.FilterIPv6)
	m.WhitelistHOS = types.BoolValue(t.WhitelistHOS)
	m.IsDefault = types.BoolValue(t.IsDefault)
	m.InputRules = firewallRulesList(t.Rules.Input)
	m.OutputRules = firewallRulesList(t.Rules.Output)
}

func firewallTemplatePath(id int64) string { return "/firewall/template/" + strconv.FormatInt(id, 10) }

// readFirewallTemplate reads one template. The documented single-template
// answers carry no name, so a missing one is taken from the list.
func readFirewallTemplate(ctx context.Context, c *client.Client, id int64) (apiFirewallTemplate, error) {
	t, err := client.GetObject[apiFirewallTemplate](ctx, c, firewallTemplatePath(id), "firewall_template", nil)
	if err != nil || t.Name != "" {
		return t, err
	}
	list, err := client.GetList[apiFirewallTemplate](ctx, c, "/firewall/template", "firewall_template", nil)
	if err != nil {
		return t, err
	}
	for _, x := range list {
		if x.ID == id {
			t.Name = x.Name
		}
	}
	return t, nil
}

func (r *firewallTemplateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_template"
}

func (r *firewallTemplateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.List{listplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "A reusable firewall template. Apply it to a server with `hetznerrobot_firewall.template_id`; " +
			"servers it was applied to keep their rules when the template changes later.",
		Attributes: map[string]schema.Attribute{
			"id":            stableInt("Numeric template id."),
			"name":          reqStr("Name of the template."),
			"filter_ipv6":   optBool("Whether IPv6 traffic is filtered too."),
			"whitelist_hos": optBool("Whether Hetzner services are always let through. Defaults to `true` on creation."),
			"is_default":    optBool("Whether the Robot panel preselects this template."),
			"input_rules":   firewallRuleSchema("Rules for incoming packets, matched in order.", true, keep),
			"output_rules":  firewallRuleSchema("Rules for outgoing packets, matched in order.", true, keep),
		},
	}
}

func (r *firewallTemplateResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg firewallTemplateModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	validateFirewallRules(ctx, "input_rules", cfg.InputRules, &resp.Diagnostics)
	validateFirewallRules(ctx, "output_rules", cfg.OutputRules, &resp.Diagnostics)
}

// form sends the whole template: every write replaces the rule set.
func (m *firewallTemplateModel) form(ctx context.Context, diags *diag.Diagnostics) url.Values {
	f := url.Values{"name": {m.Name.ValueString()}}
	formBool(f, "filter_ipv6", m.FilterIPv6)
	if m.WhitelistHOS.IsUnknown() {
		f.Set("whitelist_hos", "true")
	} else {
		formBool(f, "whitelist_hos", m.WhitelistHOS)
	}
	formBool(f, "is_default", m.IsDefault)
	formFirewallRules(f, "input", firewallRuleModels(ctx, m.InputRules, diags))
	formFirewallRules(f, "output", firewallRuleModels(ctx, m.OutputRules, diags))
	return f
}

func (r *firewallTemplateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan firewallTemplateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	form := plan.form(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := client.SendObject[apiFirewallTemplate](ctx, r.client, http.MethodPost, "/firewall/template", "firewall_template", form)
	if err != nil {
		resp.Diagnostics.AddError("Error creating firewall template", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), created.ID)...)
	got, err := readFirewallTemplate(ctx, r.client, created.ID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading created firewall template", err.Error())
		return
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *firewallTemplateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state firewallTemplateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readFirewallTemplate(ctx, r.client, state.ID.ValueInt64())
	if client.NotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall template", err.Error())
		return
	}
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *firewallTemplateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state firewallTemplateModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueInt64()
	form := plan.form(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.Send(ctx, http.MethodPost, firewallTemplatePath(id), form, nil); err != nil {
		resp.Diagnostics.AddError("Error updating firewall template", err.Error())
		return
	}
	got, err := readFirewallTemplate(ctx, r.client, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading updated firewall template", err.Error())
		return
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *firewallTemplateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state firewallTemplateModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodDelete, firewallTemplatePath(state.ID.ValueInt64()), nil, nil)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error deleting firewall template", err.Error())
	}
}

func (r *firewallTemplateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := parseInt64(req.ID, "template id")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

// --- data sources ---

func firewallTemplateSummaryDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"id":            dsInt("Numeric template id."),
		"name":          dsStr("Name of the template."),
		"filter_ipv6":   dsBool("Whether IPv6 traffic is filtered too."),
		"whitelist_hos": dsBool("Whether Hetzner services are always let through."),
		"is_default":    dsBool("Whether the Robot panel preselects this template."),
	}
}

// NewFirewallTemplateDataSource reads one firewall template by id or name.
func NewFirewallTemplateDataSource() datasource.DataSource { return &firewallTemplateDataSource{} }

type firewallTemplateDataSource struct{ dataSourceBase }

func (d *firewallTemplateDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_template"
}

func (d *firewallTemplateDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads one firewall template, with its rules, by `id` or by `name` (exactly one of them).",
		Attributes: with(firewallTemplateSummaryDSAttrs(), map[string]dschema.Attribute{
			"id":           dsLookupInt("Numeric template id."),
			"name":         dsLookupStr("Name of the template."),
			"input_rules":  dsList("Rules for incoming packets.", dsFirewallRuleAttrs()),
			"output_rules": dsList("Rules for outgoing packets.", dsFirewallRuleAttrs()),
		}),
	}
}

func (d *firewallTemplateDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg firewallTemplateModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var id int64
	switch {
	case !cfg.ID.IsNull():
		id = cfg.ID.ValueInt64()
	case !cfg.Name.IsNull():
		list, err := client.GetList[apiFirewallTemplate](ctx, d.client, "/firewall/template", "firewall_template", nil)
		if err != nil {
			resp.Diagnostics.AddError("Error listing firewall templates", err.Error())
			return
		}
		for _, t := range list {
			if t.Name == cfg.Name.ValueString() {
				id = t.ID
				break
			}
		}
		if id == 0 {
			resp.Diagnostics.AddAttributeError(path.Root("name"), "Firewall template not found",
				fmt.Sprintf("Robot has no firewall template named %q", cfg.Name.ValueString()))
			return
		}
	default:
		resp.Diagnostics.AddError("Missing lookup key", "Set either `id` or `name`.")
		return
	}
	got, err := readFirewallTemplate(ctx, d.client, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading firewall template", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewFirewallTemplatesDataSource lists the firewall templates.
func NewFirewallTemplatesDataSource() datasource.DataSource { return &firewallTemplatesDataSource{} }

type firewallTemplatesDataSource struct{ dataSourceBase }

type firewallTemplateSummaryModel struct {
	ID           types.Int64  `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	FilterIPv6   types.Bool   `tfsdk:"filter_ipv6"`
	WhitelistHOS types.Bool   `tfsdk:"whitelist_hos"`
	IsDefault    types.Bool   `tfsdk:"is_default"`
}

type firewallTemplatesModel struct {
	Templates []firewallTemplateSummaryModel `tfsdk:"templates"`
}

func (d *firewallTemplatesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_firewall_templates"
}

func (d *firewallTemplatesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the firewall templates of the account (without their rules).",
		Attributes: map[string]dschema.Attribute{
			"templates": dsList("The firewall templates.", firewallTemplateSummaryDSAttrs()),
		},
	}
}

func (d *firewallTemplatesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := client.GetList[apiFirewallTemplate](ctx, d.client, "/firewall/template", "firewall_template", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error listing firewall templates", err.Error())
		return
	}
	out := firewallTemplatesModel{Templates: make([]firewallTemplateSummaryModel, len(list))}
	for i, t := range list {
		out.Templates[i] = firewallTemplateSummaryModel{
			ID: types.Int64Value(t.ID), Name: types.StringValue(t.Name), FilterIPv6: types.BoolValue(t.FilterIPv6),
			WhitelistHOS: types.BoolValue(t.WhitelistHOS), IsDefault: types.BoolValue(t.IsDefault),
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}
