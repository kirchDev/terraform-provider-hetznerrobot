package provider

import (
	"context"
	"fmt"
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

var (
	_ resource.Resource                = (*sshKeyResource)(nil)
	_ resource.ResourceWithImportState = (*sshKeyResource)(nil)
)

// NewSSHKeyResource manages an SSH key stored in Robot.
func NewSSHKeyResource() resource.Resource { return &sshKeyResource{} }

type sshKeyResource struct{ resourceBase }

type apiSSHKey struct {
	Name        string            `json:"name"`
	Fingerprint string            `json:"fingerprint"`
	Type        string            `json:"type"`
	Size        client.FlexInt    `json:"size"`
	Data        string            `json:"data"`
	CreatedAt   client.FlexString `json:"created_at"`
}

type sshKeyModel struct {
	Fingerprint types.String `tfsdk:"fingerprint"`
	Name        types.String `tfsdk:"name"`
	Data        types.String `tfsdk:"data"`
	Type        types.String `tfsdk:"type"`
	Size        types.Int64  `tfsdk:"size"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

// fill copies the API's view into the model. The key data is taken only when
// the model has none (import, data sources): Robot answers in OpenSSH format,
// and a key configured in SSH2 format or with a different comment would
// otherwise never match its configuration.
func (m *sshKeyModel) fill(k apiSSHKey) {
	m.Fingerprint = types.StringValue(k.Fingerprint)
	m.Name = types.StringValue(k.Name)
	if m.Data.IsNull() || m.Data.IsUnknown() {
		m.Data = types.StringValue(k.Data)
	}
	m.Type = types.StringValue(k.Type)
	m.Size = flexInt(k.Size)
	m.CreatedAt = flexStr(k.CreatedAt)
}

func keyPath(fingerprint string) string { return "/key/" + escape(fingerprint) }

func (r *sshKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (r *sshKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An SSH public key stored in Robot. Its fingerprint is what server orders, the Rescue System and " +
			"the Linux installation take as `authorized_keys`. Only the name can change in place; new key data replaces the key.",
		Attributes: map[string]schema.Attribute{
			"fingerprint": stableStr("MD5 fingerprint of the key (`aa:bb:…`) — its id in Robot."),
			"name":        reqStr("Name of the key."),
			"data":        reqStrNew("Public key in OpenSSH or SSH2 format."),
			"type":        stableStr("Key algorithm, e.g. `ED25519`."),
			"size":        stableInt("Key size in bits."),
			"created_at":  stableStr("Creation date."),
		},
	}
}

func (r *sshKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	form := url.Values{"name": {plan.Name.ValueString()}, "data": {plan.Data.ValueString()}}
	got, err := client.SendObject[apiSSHKey](ctx, r.client, http.MethodPost, "/key", "key", form)
	if err != nil {
		resp.Diagnostics.AddError("Error creating SSH key", err.Error())
		return
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *sshKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiSSHKey](ctx, r.client, keyPath(state.Fingerprint.ValueString()), "key", nil)
	if client.NotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading SSH key", err.Error())
		return
	}
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *sshKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state sshKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.SendObject[apiSSHKey](ctx, r.client, http.MethodPost, keyPath(state.Fingerprint.ValueString()), "key",
		url.Values{"name": {plan.Name.ValueString()}})
	if err != nil {
		resp.Diagnostics.AddError("Error renaming SSH key", err.Error())
		return
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *sshKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state sshKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodDelete, keyPath(state.Fingerprint.ValueString()), nil, nil)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error deleting SSH key", err.Error())
	}
}

func (r *sshKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("fingerprint"), req, resp)
}

// --- data sources ---

func sshKeyDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"fingerprint": dsStr("MD5 fingerprint of the key."),
		"name":        dsStr("Name of the key."),
		"data":        dsStr("Public key in OpenSSH format."),
		"type":        dsStr("Key algorithm, e.g. `ED25519`."),
		"size":        dsInt("Key size in bits."),
		"created_at":  dsStr("Creation date."),
	}
}

// NewSSHKeyDataSource reads one SSH key by fingerprint or name.
func NewSSHKeyDataSource() datasource.DataSource { return &sshKeyDataSource{} }

type sshKeyDataSource struct{ dataSourceBase }

func (d *sshKeyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (d *sshKeyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads one SSH key stored in Robot, by `fingerprint` or by `name` (exactly one of them).",
		Attributes: with(sshKeyDSAttrs(), map[string]dschema.Attribute{
			"fingerprint": dsLookupStr("MD5 fingerprint of the key."),
			"name":        dsLookupStr("Name of the key."),
		}),
	}
}

func (d *sshKeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg sshKeyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var got apiSSHKey
	switch {
	case !cfg.Fingerprint.IsNull():
		var err error
		if got, err = client.GetObject[apiSSHKey](ctx, d.client, keyPath(cfg.Fingerprint.ValueString()), "key", nil); err != nil {
			resp.Diagnostics.AddError("Error reading SSH key", err.Error())
			return
		}
	case !cfg.Name.IsNull():
		list, err := client.GetList[apiSSHKey](ctx, d.client, "/key", "key", nil)
		if err != nil {
			resp.Diagnostics.AddError("Error listing SSH keys", err.Error())
			return
		}
		found := false
		for _, k := range list {
			if k.Name == cfg.Name.ValueString() {
				got, found = k, true
				break
			}
		}
		if !found {
			resp.Diagnostics.AddAttributeError(path.Root("name"), "SSH key not found",
				fmt.Sprintf("Robot has no SSH key named %q", cfg.Name.ValueString()))
			return
		}
	default:
		resp.Diagnostics.AddError("Missing lookup key", "Set either `fingerprint` or `name`.")
		return
	}
	cfg.Data = types.StringNull()
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewSSHKeysDataSource lists every SSH key stored in Robot.
func NewSSHKeysDataSource() datasource.DataSource { return &sshKeysDataSource{} }

type sshKeysDataSource struct{ dataSourceBase }

type sshKeysModel struct {
	Keys []sshKeyModel `tfsdk:"keys"`
}

func (d *sshKeysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_keys"
}

func (d *sshKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists every SSH key stored in Robot.",
		Attributes: map[string]dschema.Attribute{
			"keys": dsList("The SSH keys.", sshKeyDSAttrs()),
		},
	}
}

func (d *sshKeysDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := client.GetList[apiSSHKey](ctx, d.client, "/key", "key", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error listing SSH keys", err.Error())
		return
	}
	out := sshKeysModel{Keys: make([]sshKeyModel, len(list))}
	for i, k := range list {
		out.Keys[i].Data = types.StringNull()
		out.Keys[i].fill(k)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}
