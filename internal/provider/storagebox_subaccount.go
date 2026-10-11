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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

var (
	_ resource.Resource                = (*storageBoxSubaccountResource)(nil)
	_ resource.ResourceWithImportState = (*storageBoxSubaccountResource)(nil)
)

// NewStorageBoxSubaccountResource manages a sub-account of a Storage Box.
func NewStorageBoxSubaccountResource() resource.Resource { return &storageBoxSubaccountResource{} }

type storageBoxSubaccountResource struct{ resourceBase }

type apiStorageBoxSubaccount struct {
	Username             string            `json:"username"`
	Password             string            `json:"password"`
	AccountID            string            `json:"accountid"`
	Server               client.FlexString `json:"server"`
	HomeDirectory        string            `json:"homedirectory"`
	Samba                bool              `json:"samba"`
	SSH                  bool              `json:"ssh"`
	ExternalReachability bool              `json:"external_reachability"`
	WebDAV               bool              `json:"webdav"`
	ReadOnly             bool              `json:"readonly"`
	CreateTime           client.FlexString `json:"createtime"`
	Comment              client.FlexString `json:"comment"`
}

type storageBoxSubaccountModel struct {
	StorageBoxID         types.Int64  `tfsdk:"storagebox_id"`
	Username             types.String `tfsdk:"username"`
	HomeDirectory        types.String `tfsdk:"home_directory"`
	Samba                types.Bool   `tfsdk:"samba"`
	SSH                  types.Bool   `tfsdk:"ssh"`
	ExternalReachability types.Bool   `tfsdk:"external_reachability"`
	WebDAV               types.Bool   `tfsdk:"webdav"`
	ReadOnly             types.Bool   `tfsdk:"readonly"`
	Comment              types.String `tfsdk:"comment"`
	Password             types.String `tfsdk:"password"`
	AccountID            types.String `tfsdk:"account_id"`
	Server               types.String `tfsdk:"server"`
	CreateTime           types.String `tfsdk:"create_time"`
}

// fill copies the API's view; the password is not part of it (the list never
// carries one) and stays as it is.
func (m *storageBoxSubaccountModel) fill(boxID int64, s apiStorageBoxSubaccount) {
	m.StorageBoxID = types.Int64Value(boxID)
	m.Username = types.StringValue(s.Username)
	m.HomeDirectory = types.StringValue(s.HomeDirectory)
	m.Samba = types.BoolValue(s.Samba)
	m.SSH = types.BoolValue(s.SSH)
	m.ExternalReachability = types.BoolValue(s.ExternalReachability)
	m.WebDAV = types.BoolValue(s.WebDAV)
	m.ReadOnly = types.BoolValue(s.ReadOnly)
	m.Comment = types.StringValue(s.Comment.Value)
	m.AccountID = types.StringValue(s.AccountID)
	m.Server = flexStr(s.Server)
	m.CreateTime = flexStr(s.CreateTime)
}

func storageBoxSubaccountPath(boxID int64, username string) string {
	return storageBoxPath(boxID) + "/subaccount/" + escape(username)
}

func listStorageBoxSubaccounts(ctx context.Context, c *client.Client, boxID int64) ([]apiStorageBoxSubaccount, error) {
	return client.GetList[apiStorageBoxSubaccount](ctx, c, storageBoxPath(boxID)+"/subaccount", "subaccount", nil)
}

// findStorageBoxSubaccount reads one sub-account through the list — there is
// no single read. ok is false when it is gone.
func findStorageBoxSubaccount(ctx context.Context, c *client.Client, boxID int64, username string) (apiStorageBoxSubaccount, bool, error) {
	list, err := listStorageBoxSubaccounts(ctx, c, boxID)
	if err != nil {
		return apiStorageBoxSubaccount{}, false, err
	}
	for _, s := range list {
		if s.Username == username {
			return s, true, nil
		}
	}
	return apiStorageBoxSubaccount{}, false, nil
}

// storageBoxSubaccountForm holds every known setting — the PUT takes them all.
func storageBoxSubaccountForm(plan storageBoxSubaccountModel) url.Values {
	form := url.Values{}
	formStr(form, "homedirectory", plan.HomeDirectory)
	formBool(form, "samba", plan.Samba)
	formBool(form, "ssh", plan.SSH)
	formBool(form, "external_reachability", plan.ExternalReachability)
	formBool(form, "webdav", plan.WebDAV)
	formBool(form, "readonly", plan.ReadOnly)
	formStr(form, "comment", plan.Comment)
	return form
}

func (r *storageBoxSubaccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storagebox_subaccount"
}

func (r *storageBoxSubaccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A sub-account of a Storage Box — an extra login confined to one directory." + storageBoxNote,
		Attributes: map[string]schema.Attribute{
			"storagebox_id":         reqIntNew("Storage Box the sub-account belongs to."),
			"username":              stableStr("User name Robot assigns, e.g. `u12345-sub1`."),
			"home_directory":        reqStr("Home directory of the sub-account, relative to the box's root."),
			"samba":                 optBool("Whether Samba (SMB/CIFS) access is enabled."),
			"ssh":                   optBool("Whether SSH access is enabled."),
			"external_reachability": optBool("Whether the sub-account is reachable from outside the Hetzner network."),
			"webdav":                optBool("Whether WebDAV access is enabled."),
			"readonly":              optBool("Whether the sub-account can only read."),
			"comment":               optStr("Comment on the sub-account."),
			"password": schema.StringAttribute{
				MarkdownDescription: "Password of the sub-account. Left out, Robot generates one on create and it is kept here; " +
					"set, it is applied whenever it changes. Robot never returns it, so after an import it is null until set. " +
					"12–128 characters with upper and lower case letters, a digit and a special character.",
				Optional:      true,
				Computed:      true,
				Sensitive:     true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"account_id":  stableStr("User name of the box's main user."),
			"server":      stableStr("Host name to connect to."),
			"create_time": stableStr("Creation time."),
		},
	}
}

func (r *storageBoxSubaccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan storageBoxSubaccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	boxID := plan.StorageBoxID.ValueInt64()
	form := storageBoxSubaccountForm(plan)
	formStr(form, "password", plan.Password)
	created, err := client.SendObject[apiStorageBoxSubaccount](ctx, r.client, http.MethodPost,
		storageBoxPath(boxID)+"/subaccount", "subaccount", form)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Storage Box sub-account", err.Error())
		return
	}
	if plan.Password.IsNull() || plan.Password.IsUnknown() {
		plan.Password = types.StringValue(created.Password)
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("storagebox_id"), boxID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username"), created.Username)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("password"), plan.Password)...)

	got, ok, err := findStorageBoxSubaccount(ctx, r.client, boxID, created.Username)
	if err == nil && !ok {
		err = fmt.Errorf("sub-account %s was created but does not appear in the sub-account list", created.Username)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading created Storage Box sub-account", err.Error())
		return
	}
	plan.fill(boxID, got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storageBoxSubaccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state storageBoxSubaccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	boxID := state.StorageBoxID.ValueInt64()
	got, ok, err := findStorageBoxSubaccount(ctx, r.client, boxID, state.Username.ValueString())
	if client.NotFound(err) || (err == nil && !ok) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Storage Box sub-account", err.Error())
		return
	}
	state.fill(boxID, got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *storageBoxSubaccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state storageBoxSubaccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	boxID, username := state.StorageBoxID.ValueInt64(), state.Username.ValueString()
	subPath := storageBoxSubaccountPath(boxID, username)

	settingsChanged := !plan.HomeDirectory.Equal(state.HomeDirectory) || !plan.Samba.Equal(state.Samba) ||
		!plan.SSH.Equal(state.SSH) || !plan.ExternalReachability.Equal(state.ExternalReachability) ||
		!plan.WebDAV.Equal(state.WebDAV) || !plan.ReadOnly.Equal(state.ReadOnly) || !plan.Comment.Equal(state.Comment)
	if settingsChanged {
		if err := r.client.Send(ctx, http.MethodPut, subPath, storageBoxSubaccountForm(plan), nil); err != nil {
			resp.Diagnostics.AddError("Error updating Storage Box sub-account", err.Error())
			return
		}
	}

	switch {
	case plan.Password.IsUnknown():
		plan.Password = state.Password
	case !plan.Password.IsNull() && !plan.Password.Equal(state.Password):
		var answer struct {
			Password string `json:"password"`
		}
		if err := r.client.Send(ctx, http.MethodPost, subPath+"/password",
			url.Values{"password": {plan.Password.ValueString()}}, &answer); err != nil {
			resp.Diagnostics.AddError("Error setting Storage Box sub-account password", err.Error())
			return
		}
	}

	got, ok, err := findStorageBoxSubaccount(ctx, r.client, boxID, username)
	if err == nil && !ok {
		err = fmt.Errorf("sub-account %s no longer exists", username)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading updated Storage Box sub-account", err.Error())
		return
	}
	plan.fill(boxID, got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storageBoxSubaccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state storageBoxSubaccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodDelete,
		storageBoxSubaccountPath(state.StorageBoxID.ValueInt64(), state.Username.ValueString()), nil, nil)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error deleting Storage Box sub-account", err.Error())
	}
}

func (r *storageBoxSubaccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, "storagebox_id", "username")
	if err == nil {
		var id int64
		if id, err = parseInt64(parts[0], "storagebox_id"); err == nil {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("storagebox_id"), id)...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("username"), parts[1])...)
			return
		}
	}
	resp.Diagnostics.AddError("Invalid import id", err.Error())
}

// NewStorageBoxSubaccountsDataSource lists the sub-accounts of a Storage Box.
func NewStorageBoxSubaccountsDataSource() datasource.DataSource {
	return &storageBoxSubaccountsDataSource{}
}

type storageBoxSubaccountsDataSource struct{ dataSourceBase }

type storageBoxSubaccountSummaryModel struct {
	Username             types.String `tfsdk:"username"`
	HomeDirectory        types.String `tfsdk:"home_directory"`
	Samba                types.Bool   `tfsdk:"samba"`
	SSH                  types.Bool   `tfsdk:"ssh"`
	ExternalReachability types.Bool   `tfsdk:"external_reachability"`
	WebDAV               types.Bool   `tfsdk:"webdav"`
	ReadOnly             types.Bool   `tfsdk:"readonly"`
	Comment              types.String `tfsdk:"comment"`
	AccountID            types.String `tfsdk:"account_id"`
	Server               types.String `tfsdk:"server"`
	CreateTime           types.String `tfsdk:"create_time"`
}

type storageBoxSubaccountsModel struct {
	StorageBoxID types.Int64                        `tfsdk:"storagebox_id"`
	Subaccounts  []storageBoxSubaccountSummaryModel `tfsdk:"subaccounts"`
}

func (d *storageBoxSubaccountsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storagebox_subaccounts"
}

func (d *storageBoxSubaccountsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the sub-accounts of a Storage Box (without passwords — Robot never returns them)." + storageBoxNote,
		Attributes: map[string]dschema.Attribute{
			"storagebox_id": dsRequiredInt("Storage Box to list."),
			"subaccounts": dsList("The sub-accounts.", map[string]dschema.Attribute{
				"username":              dsStr("User name."),
				"home_directory":        dsStr("Home directory."),
				"samba":                 dsBool("Whether Samba access is enabled."),
				"ssh":                   dsBool("Whether SSH access is enabled."),
				"external_reachability": dsBool("Whether it is reachable from outside the Hetzner network."),
				"webdav":                dsBool("Whether WebDAV access is enabled."),
				"readonly":              dsBool("Whether it can only read."),
				"comment":               dsStr("Comment."),
				"account_id":            dsStr("User name of the box's main user."),
				"server":                dsStr("Host name to connect to."),
				"create_time":           dsStr("Creation time."),
			}),
		},
	}
}

func (d *storageBoxSubaccountsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg storageBoxSubaccountsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	boxID := cfg.StorageBoxID.ValueInt64()
	list, err := listStorageBoxSubaccounts(ctx, d.client, boxID)
	if err != nil {
		resp.Diagnostics.AddError("Error listing Storage Box sub-accounts", err.Error())
		return
	}
	cfg.Subaccounts = make([]storageBoxSubaccountSummaryModel, len(list))
	for i, s := range list {
		var m storageBoxSubaccountModel
		m.fill(boxID, s)
		cfg.Subaccounts[i] = storageBoxSubaccountSummaryModel{
			Username: m.Username, HomeDirectory: m.HomeDirectory, Samba: m.Samba, SSH: m.SSH,
			ExternalReachability: m.ExternalReachability, WebDAV: m.WebDAV, ReadOnly: m.ReadOnly,
			Comment: m.Comment, AccountID: m.AccountID, Server: m.Server, CreateTime: m.CreateTime,
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
