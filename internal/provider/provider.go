package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

var _ provider.Provider = (*HetznerRobotProvider)(nil)

// HetznerRobotProvider is the provider implementation.
type HetznerRobotProvider struct {
	// version is set to the release version on build, or "dev" for local builds.
	version string
}

// HetznerRobotProviderModel maps provider schema data to a Go type.
type HetznerRobotProviderModel struct {
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
	Endpoint types.String `tfsdk:"endpoint"`
}

// New returns a function that instantiates the provider.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &HetznerRobotProvider{version: version}
	}
}

func (p *HetznerRobotProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "hetznerrobot"
	resp.Version = p.version
}

func (p *HetznerRobotProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage [Hetzner Robot](https://robot.hetzner.com) — dedicated servers, IPs, subnets, failover IPs, " +
			"reverse DNS, SSH keys, boot configurations, firewalls, vSwitches, Storage Boxes and orders — as code, " +
			"through the [Robot webservice](https://robot.hetzner.com/doc/webservice/en.html).",
		Attributes: map[string]schema.Attribute{
			"username": schema.StringAttribute{
				MarkdownDescription: "Webservice user (Robot → Settings → Web service and app settings). " +
					"May also be set via the `HETZNER_ROBOT_USER` environment variable.",
				Optional: true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password of the webservice user. May also be set via `HETZNER_ROBOT_PASSWORD`. " +
					"Robot blocks the calling IP for ten minutes after three failed logins.",
				Optional:  true,
				Sensitive: true,
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Base URL of the webservice. Defaults to `https://robot-ws.your-server.de`. " +
					"May also be set via `HETZNER_ROBOT_ENDPOINT`.",
				Optional: true,
			},
		},
	}
}

func (p *HetznerRobotProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config HetznerRobotProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for attr, v := range map[string]types.String{"username": config.Username, "password": config.Password} {
		if v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(attr), "Unknown Hetzner Robot "+attr,
				"The provider cannot create the API client because the "+attr+" is unknown. "+
					"Set it statically or via the HETZNER_ROBOT_USER / HETZNER_ROBOT_PASSWORD environment variables.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// Env vars are the default; explicit config wins.
	username := os.Getenv("HETZNER_ROBOT_USER")
	if !config.Username.IsNull() {
		username = config.Username.ValueString()
	}
	password := os.Getenv("HETZNER_ROBOT_PASSWORD")
	if !config.Password.IsNull() {
		password = config.Password.ValueString()
	}
	endpoint := os.Getenv("HETZNER_ROBOT_ENDPOINT")
	if !config.Endpoint.IsNull() && !config.Endpoint.IsUnknown() {
		endpoint = config.Endpoint.ValueString()
	}

	if username == "" {
		resp.Diagnostics.AddAttributeError(path.Root("username"), "Missing Hetzner Robot webservice user",
			"Set the provider `username` argument or the HETZNER_ROBOT_USER environment variable.")
	}
	if password == "" {
		resp.Diagnostics.AddAttributeError(path.Root("password"), "Missing Hetzner Robot webservice password",
			"Set the provider `password` argument or the HETZNER_ROBOT_PASSWORD environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c := client.New(endpoint, username, password, "terraform-provider-hetznerrobot/"+p.version)
	configureClient(c)
	resp.DataSourceData = c
	resp.ResourceData = c
}

func (p *HetznerRobotProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewBootLinuxResource,
		NewBootRescueResource,
		NewBootVNCResource,
		NewBootWindowsResource,
		NewFailoverResource,
		NewFirewallResource,
		NewFirewallTemplateResource,
		NewIPCancellationResource,
		NewIPMACResource,
		NewIPResource,
		NewRDNSResource,
		NewServerAddonOrderResource,
		NewServerCancellationResource,
		NewServerMarketOrderResource,
		NewServerOrderResource,
		NewServerResetResource,
		NewServerResource,
		NewSSHKeyResource,
		NewStorageBoxResource,
		NewStorageBoxSnapshotPlanResource,
		NewStorageBoxSnapshotResource,
		NewStorageBoxSubaccountResource,
		NewSubnetCancellationResource,
		NewSubnetMACResource,
		NewSubnetResource,
		NewVSwitchResource,
		NewWakeOnLANResource,
	}
}

func (p *HetznerRobotProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewBootDataSource,
		NewCurrencyDataSource,
		NewFailoverDataSource,
		NewFailoversDataSource,
		NewFirewallDataSource,
		NewFirewallTemplateDataSource,
		NewFirewallTemplatesDataSource,
		NewIPCancellationDataSource,
		NewIPDataSource,
		NewIPsDataSource,
		NewRDNSDataSource,
		NewRDNSEntriesDataSource,
		NewResetDataSource,
		NewResetsDataSource,
		NewServerAddonProductsDataSource,
		NewServerCancellationDataSource,
		NewServerDataSource,
		NewServerMarketProductDataSource,
		NewServerMarketProductsDataSource,
		NewServerProductDataSource,
		NewServerProductsDataSource,
		NewServersDataSource,
		NewSSHKeyDataSource,
		NewSSHKeysDataSource,
		NewStorageBoxDataSource,
		NewStorageBoxesDataSource,
		NewStorageBoxSnapshotPlanDataSource,
		NewStorageBoxSnapshotsDataSource,
		NewStorageBoxSubaccountsDataSource,
		NewSubnetCancellationDataSource,
		NewSubnetDataSource,
		NewSubnetMACDataSource,
		NewSubnetsDataSource,
		NewTrafficDataSource,
		NewVSwitchDataSource,
		NewVSwitchesDataSource,
	}
}
