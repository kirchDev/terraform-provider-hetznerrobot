package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

// bootKind names what differs between the four boot configurations; one
// implementation serves them all.
type bootKind struct {
	// name is the path segment and the response key: rescue, linux, vnc, windows.
	name string
	// choice is the attribute and form key of the main choice: os or dist.
	choice      string
	choiceDesc  string
	hasLang     bool
	hasKeys     bool // authorized_keys in, host_keys out
	hasKeyboard bool
	description string
}

var (
	bootRescue = bootKind{
		name: "rescue", choice: "os", choiceDesc: "Rescue System to boot, e.g. `linux` or `vkvm` (see `hetznerrobot_boot.rescue_os`).",
		hasKeys: true, hasKeyboard: true,
		description: "Activates the Rescue System of a dedicated server. It takes effect on the next reboot — pair it with " +
			"`hetznerrobot_server_reset` — and is used up by that boot: Robot then reports `active = false`, which the resource " +
			"keeps as a fact rather than activating the system again. Only one boot configuration can be active at a time. " +
			"Destroying the resource deactivates the Rescue System if it is still pending.",
	}
	bootLinux = bootKind{
		name: "linux", choice: "dist", choiceDesc: "Distribution to install (see `hetznerrobot_boot.linux_dists`).",
		hasLang: true, hasKeys: true,
		description: "Activates the automatic Linux installation of a dedicated server. **The installation wipes the server.** " +
			"It runs on the next reboot — pair it with `hetznerrobot_server_reset` — after which Robot reports `active = false`; " +
			"the resource keeps that as a fact and never installs again on its own. Only one boot configuration can be active " +
			"at a time. Destroying the resource deactivates the installation if it is still pending.",
	}
	bootVNC = bootKind{
		name: "vnc", choice: "dist", choiceDesc: "Distribution to install over VNC (see `hetznerrobot_boot.vnc_dists`).",
		hasLang: true,
		description: "Activates a VNC-guided installation of a dedicated server. **The installation wipes the server.** " +
			"It starts on the next reboot — pair it with `hetznerrobot_server_reset` — after which Robot reports `active = false`; " +
			"the resource keeps that as a fact. Only one boot configuration can be active at a time. Destroying the resource " +
			"deactivates the installation if it is still pending.",
	}
	bootWindows = bootKind{
		name: "windows", choice: "os", choiceDesc: "Windows version to install (see `hetznerrobot_boot.windows_os`).",
		hasLang: true,
		description: "Activates the Windows installation of a dedicated server; the Windows addon has to be ordered for the " +
			"server first. **The installation wipes the server.** It starts on the next reboot — pair it with " +
			"`hetznerrobot_server_reset` — after which Robot reports `active = false`; the resource keeps that as a fact. " +
			"Only one boot configuration can be active at a time. Destroying the resource deactivates the installation if it " +
			"is still pending.",
	}
)

// NewBootRescueResource activates the Rescue System.
func NewBootRescueResource() resource.Resource { return &bootResource{kind: bootRescue} }

// NewBootLinuxResource activates the Linux installation.
func NewBootLinuxResource() resource.Resource { return &bootResource{kind: bootLinux} }

// NewBootVNCResource activates the VNC installation.
func NewBootVNCResource() resource.Resource { return &bootResource{kind: bootVNC} }

// NewBootWindowsResource activates the Windows installation.
func NewBootWindowsResource() resource.Resource { return &bootResource{kind: bootWindows} }

var (
	_ resource.Resource                = (*bootResource)(nil)
	_ resource.ResourceWithImportState = (*bootResource)(nil)
)

type bootResource struct {
	resourceBase
	kind bootKind
}

// apiBootConfig is one boot configuration. os/dist/lang are the arrays of the
// available choices while inactive and the chosen string while active.
type apiBootConfig struct {
	ServerIP      string              `json:"server_ip"`
	ServerIPv6Net string              `json:"server_ipv6_net"`
	OS            client.StringOrList `json:"os"`
	Dist          client.StringOrList `json:"dist"`
	Lang          client.StringOrList `json:"lang"`
	Active        bool                `json:"active"`
	Password      *string             `json:"password"`
	AuthorizedKey []json.RawMessage   `json:"authorized_key"`
	HostKey       []json.RawMessage   `json:"host_key"`
}

func (a apiBootConfig) choice(k bootKind) client.StringOrList {
	if k.choice == "dist" {
		return a.Dist
	}
	return a.OS
}

// bootKeyFingerprints reads authorized_key/host_key entries. The documentation
// shows them only empty; the order answers wrap a key as {"key": {…}}, so that
// shape, a bare object and a plain string are all taken.
func bootKeyFingerprints(raw []json.RawMessage) []string {
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		var s string
		if json.Unmarshal(r, &s) == nil {
			out = append(out, s)
			continue
		}
		var wrapped struct {
			Key struct {
				Fingerprint string `json:"fingerprint"`
			} `json:"key"`
			Fingerprint string `json:"fingerprint"`
		}
		if json.Unmarshal(r, &wrapped) == nil {
			if wrapped.Key.Fingerprint != "" {
				out = append(out, wrapped.Key.Fingerprint)
			} else if wrapped.Fingerprint != "" {
				out = append(out, wrapped.Fingerprint)
			}
		}
	}
	return out
}

func bootPath(serverNumber int64, kind string) string {
	return "/boot/" + strconv.FormatInt(serverNumber, 10) + "/" + kind
}

func (r *bootResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_boot_" + r.kind.name
}

func (r *bootResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	k := r.kind
	attrs := map[string]schema.Attribute{
		"server_number":   reqIntNew("Server to configure."),
		k.choice:          reqStrNew(k.choiceDesc),
		"server_ip":       stableStr("Main IP address of the server."),
		"server_ipv6_net": stableStr("Main IPv6 net of the server."),
		"password": sensitiveStable("Root (or Administrator) password Robot generated on activation. Only the activation " +
			"answer carries it, so it stays as it was after the configuration is used up."),
		"active": computedBool("Whether the configuration is still pending. Robot clears it once the server has booted it."),
	}
	if k.hasLang {
		attrs["lang"] = reqStrNew("Language of the installation, e.g. `en`.")
	}
	if k.hasKeys {
		attrs["authorized_keys"] = schema.SetAttribute{
			MarkdownDescription: "Fingerprints of SSH keys stored in Robot (`hetznerrobot_ssh_key.fingerprint`) to " +
				"authorize for root instead of relying on the password. Changing them activates the configuration anew; " +
				"setting them after an import that could not read them does not.",
			Optional:      true,
			ElementType:   types.StringType,
			PlanModifiers: []planmodifier.Set{replaceOnSetChange{}},
		}
		attrs["host_keys"] = stableStrList("Host key fingerprints of the booted system, as far as Robot reports them.")
	}
	if k.hasKeyboard {
		attrs["keyboard"] = schema.StringAttribute{
			MarkdownDescription: "Keyboard layout, e.g. `de`. Robot defaults to `us`. Robot does not report it back, " +
				"so setting it after an import does not activate the configuration anew; changing it does.",
			Optional:      true,
			PlanModifiers: []planmodifier.String{replaceOnSetChange{}},
		}
	}
	resp.Schema = schema.Schema{MarkdownDescription: k.description, Attributes: attrs}
}

// bootValues are the arguments of a boot configuration, read by path because
// the four kinds have different schemas.
type bootValues struct {
	ServerNumber types.Int64
	Choice       types.String
	Lang         types.String
	Keyboard     types.String
	Keys         types.Set
}

func (r *bootResource) values(ctx context.Context, get func(context.Context, path.Path, any) diag.Diagnostics, diags *diag.Diagnostics) bootValues {
	var v bootValues
	diags.Append(get(ctx, path.Root("server_number"), &v.ServerNumber)...)
	diags.Append(get(ctx, path.Root(r.kind.choice), &v.Choice)...)
	if r.kind.hasLang {
		diags.Append(get(ctx, path.Root("lang"), &v.Lang)...)
	}
	if r.kind.hasKeyboard {
		diags.Append(get(ctx, path.Root("keyboard"), &v.Keyboard)...)
	}
	if r.kind.hasKeys {
		diags.Append(get(ctx, path.Root("authorized_keys"), &v.Keys)...)
	}
	return v
}

// setComputed writes what Robot reports into state. withSecrets is false for
// a read: the password and the host keys are only trustworthy in the
// activation answer.
func (r *bootResource) setComputed(ctx context.Context, st *tfsdk.State, got apiBootConfig, withSecrets bool, diags *diag.Diagnostics) {
	diags.Append(st.SetAttribute(ctx, path.Root("server_ip"), got.ServerIP)...)
	diags.Append(st.SetAttribute(ctx, path.Root("server_ipv6_net"), got.ServerIPv6Net)...)
	diags.Append(st.SetAttribute(ctx, path.Root("active"), got.Active)...)
	if !withSecrets {
		return
	}
	diags.Append(st.SetAttribute(ctx, path.Root("password"), strPtr(got.Password))...)
	if r.kind.hasKeys {
		diags.Append(st.SetAttribute(ctx, path.Root("host_keys"), stringList(bootKeyFingerprints(got.HostKey)))...)
	}
}

func (r *bootResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	v := r.values(ctx, req.Plan.GetAttribute, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	form := url.Values{r.kind.choice: {v.Choice.ValueString()}}
	formStr(form, "lang", v.Lang)
	formStr(form, "keyboard", v.Keyboard)
	formArray(form, "authorized_key", valueStrings(ctx, v.Keys, &resp.Diagnostics))
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.SendObject[apiBootConfig](ctx, r.client, http.MethodPost,
		bootPath(v.ServerNumber.ValueInt64(), r.kind.name), r.kind.name, form)
	if err != nil {
		resp.Diagnostics.AddError("Error activating "+r.kind.name+" boot configuration", err.Error())
		return
	}
	resp.State.Raw = req.Plan.Raw
	r.setComputed(ctx, &resp.State, got, true, &resp.Diagnostics)
}

// Read refreshes only the activation status. Once the server has booted the
// configuration Robot reports it inactive; the resource stays, so the next
// plan does not activate it again.
func (r *bootResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	v := r.values(ctx, req.State.GetAttribute, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiBootConfig](ctx, r.client, bootPath(v.ServerNumber.ValueInt64(), r.kind.name), r.kind.name, nil)
	switch {
	case client.HasCode(err, "SERVER_NOT_FOUND"):
		resp.State.RemoveResource(ctx)
		return
	case client.HasCode(err, "BOOT_NOT_AVAILABLE"):
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("active"), false)...)
		return
	case err != nil:
		resp.Diagnostics.AddError("Error reading "+r.kind.name+" boot configuration", err.Error())
		return
	}
	r.setComputed(ctx, &resp.State, got, false, &resp.Diagnostics)
}

// Update is never called with a change — every argument replaces — but the
// framework requires it.
// Update only records keyboard or authorized_keys set or unset after the fact —
// a real change of either replaces the resource. Everything else stays as the
// state has it (the plan holds the computed values as unknown).
func (r *bootResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.State.Raw = req.State.Raw
	for name, has := range map[string]bool{"keyboard": r.kind.hasKeyboard, "authorized_keys": r.kind.hasKeys} {
		if !has {
			continue
		}
		var v attr.Value
		resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root(name), &v)...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(name), v)...)
	}
}

func (r *bootResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	v := r.values(ctx, req.State.GetAttribute, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	p := bootPath(v.ServerNumber.ValueInt64(), r.kind.name)
	got, err := client.GetObject[apiBootConfig](ctx, r.client, p, r.kind.name, nil)
	if client.NotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading "+r.kind.name+" boot configuration", err.Error())
		return
	}
	if !got.Active {
		return // already used up by a boot; nothing to deactivate
	}
	if err := r.client.Send(ctx, http.MethodDelete, p, nil, nil); err != nil && !client.NotFound(err) {
		resp.Diagnostics.AddError("Error deactivating "+r.kind.name+" boot configuration", err.Error())
	}
}

// ImportState adopts a pending configuration. An inactive one has nothing to
// adopt — Robot no longer says what was chosen.
func (r *bootResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	n, err := parseInt64(req.ID, "server_number")
	if err != nil {
		resp.Diagnostics.AddError("Invalid import id", err.Error())
		return
	}
	got, err := client.GetObject[apiBootConfig](ctx, r.client, bootPath(n, r.kind.name), r.kind.name, nil)
	if err != nil {
		resp.Diagnostics.AddError("Error reading "+r.kind.name+" boot configuration", err.Error())
		return
	}
	if !got.Active {
		resp.Diagnostics.AddError("Nothing to import",
			fmt.Sprintf("The %s boot configuration of server %d is not active, so there is no activation to adopt.", r.kind.name, n))
		return
	}
	st := &resp.State
	resp.Diagnostics.Append(st.SetAttribute(ctx, path.Root("server_number"), n)...)
	resp.Diagnostics.Append(st.SetAttribute(ctx, path.Root(r.kind.choice), got.choice(r.kind).Single)...)
	if r.kind.hasLang {
		resp.Diagnostics.Append(st.SetAttribute(ctx, path.Root("lang"), got.Lang.Single)...)
	}
	if r.kind.hasKeys {
		if keys := bootKeyFingerprints(got.AuthorizedKey); len(keys) > 0 {
			resp.Diagnostics.Append(st.SetAttribute(ctx, path.Root("authorized_keys"), stringSet(keys))...)
		}
	}
	r.setComputed(ctx, st, got, true, &resp.Diagnostics)
}

// --- data source ---

// NewBootDataSource reads the boot options of a server.
func NewBootDataSource() datasource.DataSource { return &bootDataSource{} }

type bootDataSource struct{ dataSourceBase }

type bootDSModel struct {
	ServerNumber  types.Int64 `tfsdk:"server_number"`
	RescueOS      types.List  `tfsdk:"rescue_os"`
	RescueActive  types.Bool  `tfsdk:"rescue_active"`
	LinuxDists    types.List  `tfsdk:"linux_dists"`
	LinuxLangs    types.List  `tfsdk:"linux_langs"`
	LinuxActive   types.Bool  `tfsdk:"linux_active"`
	VNCDists      types.List  `tfsdk:"vnc_dists"`
	VNCLangs      types.List  `tfsdk:"vnc_langs"`
	VNCActive     types.Bool  `tfsdk:"vnc_active"`
	WindowsOS     types.List  `tfsdk:"windows_os"`
	WindowsLangs  types.List  `tfsdk:"windows_langs"`
	WindowsActive types.Bool  `tfsdk:"windows_active"`
}

func (d *bootDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_boot"
}

func (d *bootDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads the boot options of a dedicated server — the Rescue Systems, distributions, Windows " +
			"versions and languages its boot configurations accept, and which configuration is pending. While a " +
			"configuration is active, Robot reports only its chosen value instead of the full list. Passwords are never exposed.",
		Attributes: map[string]dschema.Attribute{
			"server_number":  dsRequiredInt("Server to read."),
			"rescue_os":      dsStrList("Rescue Systems (`os` of `hetznerrobot_boot_rescue`)."),
			"rescue_active":  dsBool("Whether the Rescue System is pending."),
			"linux_dists":    dsStrList("Distributions of the Linux installation."),
			"linux_langs":    dsStrList("Languages of the Linux installation."),
			"linux_active":   dsBool("Whether a Linux installation is pending."),
			"vnc_dists":      dsStrList("Distributions of the VNC installation."),
			"vnc_langs":      dsStrList("Languages of the VNC installation."),
			"vnc_active":     dsBool("Whether a VNC installation is pending."),
			"windows_os":     dsStrList("Windows versions (needs the Windows addon; empty when it is not available)."),
			"windows_langs":  dsStrList("Languages of the Windows installation."),
			"windows_active": dsBool("Whether a Windows installation is pending."),
		},
	}
}

func (d *bootDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg bootDSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[struct {
		Rescue  *apiBootConfig `json:"rescue"`
		Linux   *apiBootConfig `json:"linux"`
		VNC     *apiBootConfig `json:"vnc"`
		Windows *apiBootConfig `json:"windows"`
	}](ctx, d.client, "/boot/"+strconv.FormatInt(cfg.ServerNumber.ValueInt64(), 10), "boot", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error reading boot options", err.Error())
		return
	}
	// A configuration Robot does not offer for the server comes back as null.
	or := func(c *apiBootConfig) apiBootConfig {
		if c == nil {
			return apiBootConfig{}
		}
		return *c
	}
	rescue, linux, vnc, windows := or(got.Rescue), or(got.Linux), or(got.VNC), or(got.Windows)
	cfg.RescueOS, cfg.RescueActive = stringList(rescue.OS.Values()), types.BoolValue(rescue.Active)
	cfg.LinuxDists, cfg.LinuxLangs, cfg.LinuxActive = stringList(linux.Dist.Values()), stringList(linux.Lang.Values()), types.BoolValue(linux.Active)
	cfg.VNCDists, cfg.VNCLangs, cfg.VNCActive = stringList(vnc.Dist.Values()), stringList(vnc.Lang.Values()), types.BoolValue(vnc.Active)
	cfg.WindowsOS, cfg.WindowsLangs, cfg.WindowsActive = stringList(windows.OS.Values()), stringList(windows.Lang.Values()), types.BoolValue(windows.Active)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}
