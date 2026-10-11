package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

// storageBoxNote is appended to every Storage Box description: the Robot
// endpoints only still serve the inclusive box of a dedicated server.
const storageBoxNote = " Only the inclusive **BX10 (100 GB)** Storage Box of a dedicated server is still managed through Robot; " +
	"every other Storage Box moved to the [Hetzner Console API](https://docs.hetzner.cloud/reference/hetzner#storage-boxes) " +
	"(the `hcloud` provider). Every Storage Box write is limited to one request per 5 seconds, which the provider waits out."

var (
	_ resource.Resource                = (*storageBoxResource)(nil)
	_ resource.ResourceWithImportState = (*storageBoxResource)(nil)
)

// NewStorageBoxResource adopts an existing Storage Box and manages its settings.
func NewStorageBoxResource() resource.Resource { return &storageBoxResource{} }

type storageBoxResource struct{ resourceBase }

type apiStorageBox struct {
	ID                   int64             `json:"id"`
	Login                string            `json:"login"`
	Name                 string            `json:"name"`
	Product              string            `json:"product"`
	Cancelled            bool              `json:"cancelled"`
	Locked               bool              `json:"locked"`
	Location             string            `json:"location"`
	LinkedServer         client.FlexInt    `json:"linked_server"`
	PaidUntil            client.FlexString `json:"paid_until"`
	DiskQuota            client.FlexInt    `json:"disk_quota"`
	DiskUsage            client.FlexInt    `json:"disk_usage"`
	DiskUsageData        client.FlexInt    `json:"disk_usage_data"`
	DiskUsageSnapshots   client.FlexInt    `json:"disk_usage_snapshots"`
	WebDAV               bool              `json:"webdav"`
	Samba                bool              `json:"samba"`
	SSH                  bool              `json:"ssh"`
	ExternalReachability bool              `json:"external_reachability"`
	ZFS                  bool              `json:"zfs"`
	Server               client.FlexString `json:"server"`
	HostSystem           client.FlexString `json:"host_system"`
}

// storageBoxDSModel is a Storage Box as the data source shows it; the
// resource adds the write-only password.
type storageBoxDSModel struct {
	ID                   types.Int64  `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	Samba                types.Bool   `tfsdk:"samba"`
	WebDAV               types.Bool   `tfsdk:"webdav"`
	SSH                  types.Bool   `tfsdk:"ssh"`
	ExternalReachability types.Bool   `tfsdk:"external_reachability"`
	ZFS                  types.Bool   `tfsdk:"zfs"`
	Login                types.String `tfsdk:"login"`
	Product              types.String `tfsdk:"product"`
	Location             types.String `tfsdk:"location"`
	Server               types.String `tfsdk:"server"`
	HostSystem           types.String `tfsdk:"host_system"`
	Cancelled            types.Bool   `tfsdk:"cancelled"`
	Locked               types.Bool   `tfsdk:"locked"`
	LinkedServer         types.Int64  `tfsdk:"linked_server"`
	PaidUntil            types.String `tfsdk:"paid_until"`
	DiskQuota            types.Int64  `tfsdk:"disk_quota"`
	DiskUsage            types.Int64  `tfsdk:"disk_usage"`
	DiskUsageData        types.Int64  `tfsdk:"disk_usage_data"`
	DiskUsageSnapshots   types.Int64  `tfsdk:"disk_usage_snapshots"`
}

type storageBoxModel struct {
	ID                   types.Int64  `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	Samba                types.Bool   `tfsdk:"samba"`
	WebDAV               types.Bool   `tfsdk:"webdav"`
	SSH                  types.Bool   `tfsdk:"ssh"`
	ExternalReachability types.Bool   `tfsdk:"external_reachability"`
	ZFS                  types.Bool   `tfsdk:"zfs"`
	Password             types.String `tfsdk:"password"`
	Login                types.String `tfsdk:"login"`
	Product              types.String `tfsdk:"product"`
	Location             types.String `tfsdk:"location"`
	Server               types.String `tfsdk:"server"`
	HostSystem           types.String `tfsdk:"host_system"`
	Cancelled            types.Bool   `tfsdk:"cancelled"`
	Locked               types.Bool   `tfsdk:"locked"`
	LinkedServer         types.Int64  `tfsdk:"linked_server"`
	PaidUntil            types.String `tfsdk:"paid_until"`
	DiskQuota            types.Int64  `tfsdk:"disk_quota"`
	DiskUsage            types.Int64  `tfsdk:"disk_usage"`
	DiskUsageData        types.Int64  `tfsdk:"disk_usage_data"`
	DiskUsageSnapshots   types.Int64  `tfsdk:"disk_usage_snapshots"`
}

func (m *storageBoxDSModel) fill(b apiStorageBox) {
	m.ID = types.Int64Value(b.ID)
	m.Name = types.StringValue(b.Name)
	m.Samba = types.BoolValue(b.Samba)
	m.WebDAV = types.BoolValue(b.WebDAV)
	m.SSH = types.BoolValue(b.SSH)
	m.ExternalReachability = types.BoolValue(b.ExternalReachability)
	m.ZFS = types.BoolValue(b.ZFS)
	m.Login = types.StringValue(b.Login)
	m.Product = types.StringValue(b.Product)
	m.Location = types.StringValue(b.Location)
	m.Server = flexStr(b.Server)
	m.HostSystem = flexStr(b.HostSystem)
	m.Cancelled = types.BoolValue(b.Cancelled)
	m.Locked = types.BoolValue(b.Locked)
	m.LinkedServer = flexInt(b.LinkedServer)
	m.PaidUntil = flexStr(b.PaidUntil)
	m.DiskQuota = flexInt(b.DiskQuota)
	m.DiskUsage = flexInt(b.DiskUsage)
	m.DiskUsageData = flexInt(b.DiskUsageData)
	m.DiskUsageSnapshots = flexInt(b.DiskUsageSnapshots)
}

// fill copies the API's view; the password is never read back.
func (m *storageBoxModel) fill(b apiStorageBox) {
	var d storageBoxDSModel
	d.fill(b)
	m.ID, m.Name, m.Samba, m.WebDAV, m.SSH = d.ID, d.Name, d.Samba, d.WebDAV, d.SSH
	m.ExternalReachability, m.ZFS, m.Login, m.Product, m.Location = d.ExternalReachability, d.ZFS, d.Login, d.Product, d.Location
	m.Server, m.HostSystem, m.Cancelled, m.Locked, m.LinkedServer = d.Server, d.HostSystem, d.Cancelled, d.Locked, d.LinkedServer
	m.PaidUntil, m.DiskQuota, m.DiskUsage = d.PaidUntil, d.DiskQuota, d.DiskUsage
	m.DiskUsageData, m.DiskUsageSnapshots = d.DiskUsageData, d.DiskUsageSnapshots
}

func storageBoxPath(id int64) string { return fmt.Sprintf("/storagebox/%d", id) }

func readStorageBox(ctx context.Context, c *client.Client, id int64) (apiStorageBox, error) {
	return client.GetObject[apiStorageBox](ctx, c, storageBoxPath(id), "storagebox", nil)
}

// storageBoxSettingsForm holds the settings of plan that are known and differ
// from current — only those are sent, because every write costs 5 seconds.
func storageBoxSettingsForm(plan, current storageBoxModel) url.Values {
	form := url.Values{}
	if !plan.Name.IsUnknown() && !plan.Name.IsNull() && !plan.Name.Equal(current.Name) {
		form.Set("storagebox_name", plan.Name.ValueString())
	}
	for _, s := range []struct {
		key           string
		plan, current types.Bool
	}{
		{"samba", plan.Samba, current.Samba},
		{"webdav", plan.WebDAV, current.WebDAV},
		{"ssh", plan.SSH, current.SSH},
		{"external_reachability", plan.ExternalReachability, current.ExternalReachability},
		{"zfs", plan.ZFS, current.ZFS},
	} {
		if !s.plan.IsUnknown() && !s.plan.IsNull() && !s.plan.Equal(s.current) {
			form.Set(s.key, client.Bool(s.plan.ValueBool()))
		}
	}
	return form
}

// apply writes the changed settings and the password, then returns the box.
func (r *storageBoxResource) apply(ctx context.Context, plan, current storageBoxModel) (apiStorageBox, error) {
	id := plan.ID.ValueInt64()
	if !plan.Password.IsNull() && !plan.Password.IsUnknown() && !plan.Password.Equal(current.Password) {
		if err := r.client.Send(ctx, http.MethodPost, storageBoxPath(id)+"/password",
			url.Values{"password": {plan.Password.ValueString()}}, nil); err != nil {
			return apiStorageBox{}, fmt.Errorf("setting the password: %w", err)
		}
	}
	if form := storageBoxSettingsForm(plan, current); len(form) > 0 {
		return client.SendObject[apiStorageBox](ctx, r.client, http.MethodPost, storageBoxPath(id), "storagebox", form)
	}
	return readStorageBox(ctx, r.client, id)
}

func (r *storageBoxResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storagebox"
}

func (r *storageBoxResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The settings of an existing Storage Box. Robot cannot create one through the webservice, so this " +
			"resource adopts the box by `id`; destroying it only removes it from state." + storageBoxNote,
		Attributes: map[string]schema.Attribute{
			"id":                    reqIntNew("Storage Box id."),
			"name":                  optStr("Name of the Storage Box."),
			"samba":                 optBool("Whether Samba (SMB/CIFS) access is enabled."),
			"webdav":                optBool("Whether WebDAV access is enabled."),
			"ssh":                   optBool("Whether SSH (SFTP, SCP, rsync, BorgBackup) access is enabled."),
			"external_reachability": optBool("Whether the box is reachable from outside the Hetzner network."),
			"zfs":                   optBool("Whether the ZFS snapshot directory is visible."),
			"password": schema.StringAttribute{
				MarkdownDescription: "Password of the main user. Set, it is applied whenever it changes; left out, the password " +
					"is not touched. Robot never returns it, so a change made elsewhere goes unnoticed. 12–128 characters " +
					"with upper and lower case letters, a digit and a special character.",
				Optional:  true,
				Sensitive: true,
			},
			"login":                stableStr("Main user name, e.g. `u12345`."),
			"product":              stableStr("Product, e.g. `BX10`."),
			"location":             stableStr("Location of the box."),
			"server":               stableStr("Host name to connect to, e.g. `u12345.your-storagebox.de`."),
			"host_system":          stableStr("Identifier of the host system."),
			"cancelled":            computedBool("Whether the box is cancelled."),
			"locked":               computedBool("Whether the box is locked."),
			"linked_server":        computedInt("Number of the server the box belongs to."),
			"paid_until":           computedStr("Paid-until date."),
			"disk_quota":           computedInt("Total space in MB."),
			"disk_usage":           computedInt("Used space in MB."),
			"disk_usage_data":      computedInt("Space used by data in MB."),
			"disk_usage_snapshots": computedInt("Space used by snapshots in MB."),
		},
	}
}

func (r *storageBoxResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan storageBoxModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	box, err := readStorageBox(ctx, r.client, plan.ID.ValueInt64())
	if err != nil {
		resp.Diagnostics.AddError("Error reading Storage Box", err.Error())
		return
	}
	var current storageBoxModel
	current.fill(box)
	got, err := r.apply(ctx, plan, current)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Storage Box", err.Error())
		return
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storageBoxResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state storageBoxModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readStorageBox(ctx, r.client, state.ID.ValueInt64())
	if client.NotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Storage Box", err.Error())
		return
	}
	state.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *storageBoxResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state storageBoxModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.apply(ctx, plan, state)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Storage Box", err.Error())
		return
	}
	plan.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete only forgets the box: Robot cannot delete it, and cancelling it is a
// contract matter of the server it belongs to.
func (r *storageBoxResource) Delete(context.Context, resource.DeleteRequest, *resource.DeleteResponse) {
}

func (r *storageBoxResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := parseInt64(req.ID, "Storage Box id")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

// --- data sources ---

func storageBoxDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"id":                    dsInt("Storage Box id."),
		"name":                  dsStr("Name of the Storage Box."),
		"samba":                 dsBool("Whether Samba (SMB/CIFS) access is enabled."),
		"webdav":                dsBool("Whether WebDAV access is enabled."),
		"ssh":                   dsBool("Whether SSH access is enabled."),
		"external_reachability": dsBool("Whether the box is reachable from outside the Hetzner network."),
		"zfs":                   dsBool("Whether the ZFS snapshot directory is visible."),
		"login":                 dsStr("Main user name."),
		"product":               dsStr("Product, e.g. `BX10`."),
		"location":              dsStr("Location of the box."),
		"server":                dsStr("Host name to connect to."),
		"host_system":           dsStr("Identifier of the host system."),
		"cancelled":             dsBool("Whether the box is cancelled."),
		"locked":                dsBool("Whether the box is locked."),
		"linked_server":         dsInt("Number of the server the box belongs to."),
		"paid_until":            dsStr("Paid-until date."),
		"disk_quota":            dsInt("Total space in MB."),
		"disk_usage":            dsInt("Used space in MB."),
		"disk_usage_data":       dsInt("Space used by data in MB."),
		"disk_usage_snapshots":  dsInt("Space used by snapshots in MB."),
	}
}

// NewStorageBoxDataSource reads one Storage Box.
func NewStorageBoxDataSource() datasource.DataSource { return &storageBoxDataSource{} }

type storageBoxDataSource struct{ dataSourceBase }

func (d *storageBoxDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storagebox"
}

func (d *storageBoxDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads one Storage Box with its settings and usage." + storageBoxNote,
		Attributes: with(storageBoxDSAttrs(), map[string]dschema.Attribute{
			"id": dsRequiredInt("Storage Box id."),
		}),
	}
}

func (d *storageBoxDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg storageBoxDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := readStorageBox(ctx, d.client, cfg.ID.ValueInt64())
	if err != nil {
		resp.Diagnostics.AddError("Error reading Storage Box", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewStorageBoxesDataSource lists the account's Storage Boxes.
func NewStorageBoxesDataSource() datasource.DataSource { return &storageBoxesDataSource{} }

type storageBoxesDataSource struct{ dataSourceBase }

type storageBoxSummaryModel struct {
	ID           types.Int64  `tfsdk:"id"`
	Login        types.String `tfsdk:"login"`
	Name         types.String `tfsdk:"name"`
	Product      types.String `tfsdk:"product"`
	Cancelled    types.Bool   `tfsdk:"cancelled"`
	Locked       types.Bool   `tfsdk:"locked"`
	Location     types.String `tfsdk:"location"`
	LinkedServer types.Int64  `tfsdk:"linked_server"`
	PaidUntil    types.String `tfsdk:"paid_until"`
}

type storageBoxesModel struct {
	LinkedServer types.Int64              `tfsdk:"linked_server"`
	StorageBoxes []storageBoxSummaryModel `tfsdk:"storageboxes"`
}

func (d *storageBoxesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storageboxes"
}

func (d *storageBoxesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the account's Storage Boxes, or those of one server. The list carries no settings or " +
			"usage — read a single box with `hetznerrobot_storagebox` for those." + storageBoxNote,
		Attributes: map[string]dschema.Attribute{
			"linked_server": dschema.Int64Attribute{
				MarkdownDescription: "Number of a server; only its boxes are listed.",
				Optional:            true,
			},
			"storageboxes": dsList("The Storage Boxes.", map[string]dschema.Attribute{
				"id":            dsInt("Storage Box id."),
				"login":         dsStr("Main user name."),
				"name":          dsStr("Name of the Storage Box."),
				"product":       dsStr("Product, e.g. `BX10`."),
				"cancelled":     dsBool("Whether the box is cancelled."),
				"locked":        dsBool("Whether the box is locked."),
				"location":      dsStr("Location of the box."),
				"linked_server": dsInt("Number of the server the box belongs to."),
				"paid_until":    dsStr("Paid-until date."),
			}),
		},
	}
}

func (d *storageBoxesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg storageBoxesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := url.Values{}
	if !cfg.LinkedServer.IsNull() {
		query.Set("linked_server", strconv.FormatInt(cfg.LinkedServer.ValueInt64(), 10))
	}
	list, err := client.GetList[apiStorageBox](ctx, d.client, "/storagebox", "storagebox", query)
	if err != nil {
		resp.Diagnostics.AddError("Error listing Storage Boxes", err.Error())
		return
	}
	cfg.StorageBoxes = make([]storageBoxSummaryModel, len(list))
	for i, b := range list {
		cfg.StorageBoxes[i] = storageBoxSummaryModel{
			ID: types.Int64Value(b.ID), Login: types.StringValue(b.Login), Name: types.StringValue(b.Name),
			Product: types.StringValue(b.Product), Cancelled: types.BoolValue(b.Cancelled), Locked: types.BoolValue(b.Locked),
			Location: types.StringValue(b.Location), LinkedServer: flexInt(b.LinkedServer), PaidUntil: flexStr(b.PaidUntil),
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
