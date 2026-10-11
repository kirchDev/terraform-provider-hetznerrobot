package provider

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

// Reverting a box to a snapshot (POST …/snapshot/{name} revert=true) is
// deliberately not implemented: it is a destructive one-off action that
// overwrites the box's data, not a state a configuration could describe.

var (
	_ resource.Resource                = (*storageBoxSnapshotResource)(nil)
	_ resource.ResourceWithImportState = (*storageBoxSnapshotResource)(nil)
)

// NewStorageBoxSnapshotResource manages a manual snapshot of a Storage Box.
func NewStorageBoxSnapshotResource() resource.Resource { return &storageBoxSnapshotResource{} }

type storageBoxSnapshotResource struct{ resourceBase }

type apiStorageBoxSnapshot struct {
	Name           string            `json:"name"`
	Timestamp      client.FlexString `json:"timestamp"`
	Size           client.FlexInt    `json:"size"`
	FilesystemSize client.FlexInt    `json:"filesystem_size"`
	Automatic      bool              `json:"automatic"`
	Comment        client.FlexString `json:"comment"`
}

type storageBoxSnapshotModel struct {
	StorageBoxID   types.Int64  `tfsdk:"storagebox_id"`
	Name           types.String `tfsdk:"name"`
	Comment        types.String `tfsdk:"comment"`
	Timestamp      types.String `tfsdk:"timestamp"`
	Size           types.Int64  `tfsdk:"size"`
	FilesystemSize types.Int64  `tfsdk:"filesystem_size"`
	Automatic      types.Bool   `tfsdk:"automatic"`
}

// fill copies the API's view. An empty comment is no comment — the schema
// refuses "" so the two cannot disagree.
func (m *storageBoxSnapshotModel) fill(boxID int64, s apiStorageBoxSnapshot) {
	m.StorageBoxID = types.Int64Value(boxID)
	m.Name = types.StringValue(s.Name)
	m.Comment = types.StringNull()
	if s.Comment.Valid && s.Comment.Value != "" {
		m.Comment = types.StringValue(s.Comment.Value)
	}
	m.Timestamp = flexStr(s.Timestamp)
	m.Size = flexInt(s.Size)
	m.FilesystemSize = flexInt(s.FilesystemSize)
	m.Automatic = types.BoolValue(s.Automatic)
}

func storageBoxSnapshotPath(boxID int64, name string) string {
	return storageBoxPath(boxID) + "/snapshot/" + escape(name)
}

func listStorageBoxSnapshots(ctx context.Context, c *client.Client, boxID int64) ([]apiStorageBoxSnapshot, error) {
	return client.GetList[apiStorageBoxSnapshot](ctx, c, storageBoxPath(boxID)+"/snapshot", "snapshot", nil)
}

// findStorageBoxSnapshot reads one snapshot through the list — there is no
// single read. ok is false when it is gone.
func findStorageBoxSnapshot(ctx context.Context, c *client.Client, boxID int64, name string) (apiStorageBoxSnapshot, bool, error) {
	list, err := listStorageBoxSnapshots(ctx, c, boxID)
	if err != nil {
		return apiStorageBoxSnapshot{}, false, err
	}
	for _, s := range list {
		if s.Name == name {
			return s, true, nil
		}
	}
	return apiStorageBoxSnapshot{}, false, nil
}

func (r *storageBoxSnapshotResource) setComment(ctx context.Context, boxID int64, name string, comment types.String) error {
	return r.client.Send(ctx, http.MethodPost, storageBoxSnapshotPath(boxID, name)+"/comment",
		url.Values{"comment": {comment.ValueString()}}, nil)
}

func (r *storageBoxSnapshotResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storagebox_snapshot"
}

func (r *storageBoxSnapshotResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A manual snapshot of a Storage Box, taken when the resource is created and deleted on destroy. " +
			"Only the comment can change in place." + storageBoxNote,
		Attributes: map[string]schema.Attribute{
			"storagebox_id": reqIntNew("Storage Box the snapshot belongs to."),
			"name":          stableStr("Snapshot name, its creation time (`2026-10-11T12-00-00`)."),
			"comment": schema.StringAttribute{
				MarkdownDescription: "Comment on the snapshot.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"timestamp":       stableStr("Creation time in UTC."),
			"size":            computedInt("Size of the snapshot in MB; grows as the box's data moves away from it."),
			"filesystem_size": stableInt("Size of the box's data when the snapshot was taken, in MB."),
			"automatic": schema.BoolAttribute{
				MarkdownDescription: "Whether the snapshot plan created the snapshot (always false for this resource).",
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *storageBoxSnapshotResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan storageBoxSnapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	boxID := plan.StorageBoxID.ValueInt64()
	created, err := client.SendObject[apiStorageBoxSnapshot](ctx, r.client, http.MethodPost, storageBoxPath(boxID)+"/snapshot", "snapshot", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Storage Box snapshot", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("storagebox_id"), boxID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), created.Name)...)

	if !plan.Comment.IsNull() {
		if err := r.setComment(ctx, boxID, created.Name, plan.Comment); err != nil {
			resp.Diagnostics.AddError("Error setting Storage Box snapshot comment", err.Error())
			return
		}
	}
	got, ok, err := findStorageBoxSnapshot(ctx, r.client, boxID, created.Name)
	if err == nil && !ok {
		err = fmt.Errorf("snapshot %s was created but does not appear in the snapshot list", created.Name)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading created Storage Box snapshot", err.Error())
		return
	}
	plan.fill(boxID, got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storageBoxSnapshotResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state storageBoxSnapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	boxID := state.StorageBoxID.ValueInt64()
	got, ok, err := findStorageBoxSnapshot(ctx, r.client, boxID, state.Name.ValueString())
	if client.NotFound(err) || (err == nil && !ok) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Storage Box snapshot", err.Error())
		return
	}
	state.fill(boxID, got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *storageBoxSnapshotResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state storageBoxSnapshotModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	boxID, name := state.StorageBoxID.ValueInt64(), state.Name.ValueString()
	// A removed comment is cleared by setting an empty one.
	if err := r.setComment(ctx, boxID, name, plan.Comment); err != nil {
		resp.Diagnostics.AddError("Error updating Storage Box snapshot comment", err.Error())
		return
	}
	got, ok, err := findStorageBoxSnapshot(ctx, r.client, boxID, name)
	if err == nil && !ok {
		err = fmt.Errorf("snapshot %s no longer exists", name)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading updated Storage Box snapshot", err.Error())
		return
	}
	plan.fill(boxID, got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storageBoxSnapshotResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state storageBoxSnapshotModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodDelete, storageBoxSnapshotPath(state.StorageBoxID.ValueInt64(), state.Name.ValueString()), nil, nil)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error deleting Storage Box snapshot", err.Error())
	}
}

func (r *storageBoxSnapshotResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts, err := splitImportID(req.ID, "storagebox_id", "name")
	if err == nil {
		var id int64
		if id, err = parseInt64(parts[0], "storagebox_id"); err == nil {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("storagebox_id"), id)...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
			return
		}
	}
	resp.Diagnostics.AddError("Invalid import id", err.Error())
}

// NewStorageBoxSnapshotsDataSource lists the snapshots of a Storage Box.
func NewStorageBoxSnapshotsDataSource() datasource.DataSource {
	return &storageBoxSnapshotsDataSource{}
}

type storageBoxSnapshotsDataSource struct{ dataSourceBase }

type storageBoxSnapshotsModel struct {
	StorageBoxID types.Int64               `tfsdk:"storagebox_id"`
	Snapshots    []storageBoxSnapshotModel `tfsdk:"snapshots"`
}

func (d *storageBoxSnapshotsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storagebox_snapshots"
}

func (d *storageBoxSnapshotsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the snapshots of a Storage Box, manual and automatic." + storageBoxNote,
		Attributes: map[string]dschema.Attribute{
			"storagebox_id": dsRequiredInt("Storage Box to list."),
			"snapshots": dsList("The snapshots.", map[string]dschema.Attribute{
				"storagebox_id":   dsInt("Storage Box the snapshot belongs to."),
				"name":            dsStr("Snapshot name."),
				"comment":         dsStr("Comment on the snapshot."),
				"timestamp":       dsStr("Creation time in UTC."),
				"size":            dsInt("Size of the snapshot in MB."),
				"filesystem_size": dsInt("Size of the box's data when the snapshot was taken, in MB."),
				"automatic":       dsBool("Whether the snapshot plan created the snapshot."),
			}),
		},
	}
}

func (d *storageBoxSnapshotsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg storageBoxSnapshotsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	boxID := cfg.StorageBoxID.ValueInt64()
	list, err := listStorageBoxSnapshots(ctx, d.client, boxID)
	if err != nil {
		resp.Diagnostics.AddError("Error listing Storage Box snapshots", err.Error())
		return
	}
	cfg.Snapshots = make([]storageBoxSnapshotModel, len(list))
	for i, s := range list {
		cfg.Snapshots[i].fill(boxID, s)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// --- snapshot plan ---

var (
	_ resource.Resource                   = (*storageBoxSnapshotPlanResource)(nil)
	_ resource.ResourceWithImportState    = (*storageBoxSnapshotPlanResource)(nil)
	_ resource.ResourceWithValidateConfig = (*storageBoxSnapshotPlanResource)(nil)
)

// NewStorageBoxSnapshotPlanResource manages the automatic snapshot plan of a
// Storage Box.
func NewStorageBoxSnapshotPlanResource() resource.Resource { return &storageBoxSnapshotPlanResource{} }

type storageBoxSnapshotPlanResource struct{ resourceBase }

type apiStorageBoxSnapshotPlan struct {
	Status       string         `json:"status"`
	Minute       client.FlexInt `json:"minute"`
	Hour         client.FlexInt `json:"hour"`
	DayOfWeek    client.FlexInt `json:"day_of_week"`
	DayOfMonth   client.FlexInt `json:"day_of_month"`
	Month        client.FlexInt `json:"month"`
	MaxSnapshots client.FlexInt `json:"max_snapshots"`
}

type storageBoxSnapshotPlanModel struct {
	StorageBoxID types.Int64  `tfsdk:"storagebox_id"`
	Status       types.String `tfsdk:"status"`
	Minute       types.Int64  `tfsdk:"minute"`
	Hour         types.Int64  `tfsdk:"hour"`
	DayOfWeek    types.Int64  `tfsdk:"day_of_week"`
	DayOfMonth   types.Int64  `tfsdk:"day_of_month"`
	Month        types.Int64  `tfsdk:"month"`
	MaxSnapshots types.Int64  `tfsdk:"max_snapshots"`
}

func (m *storageBoxSnapshotPlanModel) fill(boxID int64, p apiStorageBoxSnapshotPlan) {
	m.StorageBoxID = types.Int64Value(boxID)
	m.Status = types.StringValue(p.Status)
	m.Minute = flexInt(p.Minute)
	m.Hour = flexInt(p.Hour)
	m.DayOfWeek = flexInt(p.DayOfWeek)
	m.DayOfMonth = flexInt(p.DayOfMonth)
	m.Month = flexInt(p.Month)
	m.MaxSnapshots = flexInt(p.MaxSnapshots)
}

func storageBoxSnapshotPlanPath(boxID int64) string { return storageBoxPath(boxID) + "/snapshotplan" }

// readStorageBoxSnapshotPlan reads the plan, which the API answers as a
// one-element list [{"snapshotplan": {…}}].
func readStorageBoxSnapshotPlan(ctx context.Context, c *client.Client, boxID int64) (apiStorageBoxSnapshotPlan, error) {
	var raw []byte
	var err error
	if raw, err = c.Do(ctx, http.MethodGet, storageBoxSnapshotPlanPath(boxID), nil, nil); err != nil {
		return apiStorageBoxSnapshotPlan{}, err
	}
	// The example answers with an array holding one plan, the table describes a
	// single object; both are accepted.
	var list []apiStorageBoxSnapshotPlan
	if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '{' {
		var one apiStorageBoxSnapshotPlan
		if one, err = client.Unwrap[apiStorageBoxSnapshotPlan](raw, "snapshotplan"); err == nil {
			list = append(list, one)
		}
	} else {
		list, err = client.UnwrapList[apiStorageBoxSnapshotPlan](raw, "snapshotplan")
	}
	if err == nil && len(list) == 0 {
		err = fmt.Errorf("storage box %d answered without a snapshot plan", boxID)
	}
	if err != nil {
		return apiStorageBoxSnapshotPlan{}, err
	}
	return list[0], nil
}

func (r *storageBoxSnapshotPlanResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storagebox_snapshot_plan"
}

func storageBoxScheduleAttr(desc string, lo, hi int64) schema.Int64Attribute {
	return schema.Int64Attribute{
		MarkdownDescription: desc,
		Optional:            true,
		Validators:          []validator.Int64{int64validator.Between(lo, hi)},
	}
}

func (r *storageBoxSnapshotPlanResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The automatic snapshot plan of a Storage Box — every box has exactly one, so this resource adopts " +
			"it; destroying it disables the plan. Times are in UTC; a schedule field left out matches every value " +
			"(no `day_of_week` = every day)." + storageBoxNote,
		Attributes: map[string]schema.Attribute{
			"storagebox_id": reqIntNew("Storage Box the plan belongs to."),
			"status": schema.StringAttribute{
				MarkdownDescription: "`enabled` or `disabled`. A disabled plan has no schedule, so the schedule fields must be left out.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("enabled", "disabled")},
			},
			"minute":        storageBoxScheduleAttr("Minute of the run (0–59). Required when enabled.", 0, 59),
			"hour":          storageBoxScheduleAttr("Hour of the run (0–23). Required when enabled.", 0, 23),
			"day_of_week":   storageBoxScheduleAttr("Weekday of the run, 1 = Monday … 7 = Sunday.", 1, 7),
			"day_of_month":  storageBoxScheduleAttr("Day of the month of the run (1–31).", 1, 31),
			"month":         storageBoxScheduleAttr("Month of the run, 1 = January.", 1, 12),
			"max_snapshots": optInt("Maximum number of automatic snapshots kept; the oldest is deleted beyond it."),
		},
	}
}

func (r *storageBoxSnapshotPlanResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg storageBoxSnapshotPlanModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Status.IsUnknown() || cfg.Status.IsNull() {
		return
	}
	schedule := map[string]types.Int64{
		"minute": cfg.Minute, "hour": cfg.Hour, "day_of_week": cfg.DayOfWeek,
		"day_of_month": cfg.DayOfMonth, "month": cfg.Month,
	}
	switch cfg.Status.ValueString() {
	case "enabled":
		for _, k := range []string{"minute", "hour"} {
			if schedule[k].IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root(k), "Missing snapshot plan "+k,
					"An enabled snapshot plan needs `minute` and `hour`.")
			}
		}
	case "disabled":
		for _, k := range []string{"minute", "hour", "day_of_week", "day_of_month", "month"} {
			if !schedule[k].IsNull() {
				resp.Diagnostics.AddAttributeError(path.Root(k), "Schedule on a disabled snapshot plan",
					"Robot clears the schedule of a disabled plan; leave `"+k+"` out while `status` is `disabled`.")
			}
		}
	}
}

// write applies the plan and reads it back.
func (r *storageBoxSnapshotPlanResource) write(ctx context.Context, plan *storageBoxSnapshotPlanModel) error {
	boxID := plan.StorageBoxID.ValueInt64()
	form := url.Values{"status": {plan.Status.ValueString()}}
	formInt(form, "minute", plan.Minute)
	formInt(form, "hour", plan.Hour)
	formInt(form, "day_of_week", plan.DayOfWeek)
	formInt(form, "day_of_month", plan.DayOfMonth)
	formInt(form, "month", plan.Month)
	formInt(form, "max_snapshots", plan.MaxSnapshots)
	if err := r.client.Send(ctx, http.MethodPost, storageBoxSnapshotPlanPath(boxID), form, nil); err != nil {
		return err
	}
	got, err := readStorageBoxSnapshotPlan(ctx, r.client, boxID)
	if err != nil {
		return err
	}
	plan.fill(boxID, got)
	return nil
}

func (r *storageBoxSnapshotPlanResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan storageBoxSnapshotPlanModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error setting Storage Box snapshot plan", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storageBoxSnapshotPlanResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state storageBoxSnapshotPlanModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	boxID := state.StorageBoxID.ValueInt64()
	got, err := readStorageBoxSnapshotPlan(ctx, r.client, boxID)
	if client.NotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Storage Box snapshot plan", err.Error())
		return
	}
	state.fill(boxID, got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *storageBoxSnapshotPlanResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan storageBoxSnapshotPlanModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.write(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error updating Storage Box snapshot plan", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *storageBoxSnapshotPlanResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state storageBoxSnapshotPlanModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Send(ctx, http.MethodPost, storageBoxSnapshotPlanPath(state.StorageBoxID.ValueInt64()),
		url.Values{"status": {"disabled"}}, nil)
	if err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error disabling Storage Box snapshot plan", err.Error())
	}
}

func (r *storageBoxSnapshotPlanResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := parseInt64(req.ID, "storagebox_id")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("storagebox_id"), id)...)
}

// NewStorageBoxSnapshotPlanDataSource reads the snapshot plan of a Storage Box.
func NewStorageBoxSnapshotPlanDataSource() datasource.DataSource {
	return &storageBoxSnapshotPlanDataSource{}
}

type storageBoxSnapshotPlanDataSource struct{ dataSourceBase }

func (d *storageBoxSnapshotPlanDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_storagebox_snapshot_plan"
}

func (d *storageBoxSnapshotPlanDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads the automatic snapshot plan of a Storage Box. Times are in UTC." + storageBoxNote,
		Attributes: map[string]dschema.Attribute{
			"storagebox_id": dsRequiredInt("Storage Box to read."),
			"status":        dsStr("`enabled` or `disabled`."),
			"minute":        dsInt("Minute of the run, null when disabled."),
			"hour":          dsInt("Hour of the run, null when disabled."),
			"day_of_week":   dsInt("Weekday of the run (1 = Monday), null when not set."),
			"day_of_month":  dsInt("Day of the month of the run, null when not set."),
			"month":         dsInt("Month of the run, null when not set."),
			"max_snapshots": dsInt("Maximum number of automatic snapshots kept."),
		},
	}
}

func (d *storageBoxSnapshotPlanDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg storageBoxSnapshotPlanModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	boxID := cfg.StorageBoxID.ValueInt64()
	got, err := readStorageBoxSnapshotPlan(ctx, d.client, boxID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading Storage Box snapshot plan", err.Error())
		return
	}
	cfg.fill(boxID, got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
