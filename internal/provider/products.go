package provider

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

// Prices stay strings as Robot sends them ("49.5800"): converting to a float
// would invent rounding the invoice does not have. They are in the account's
// currency (hetznerrobot_currency), net and gross (with VAT).

type apiOrderPrice struct {
	Location client.FlexString `json:"location"`
	Price    struct {
		Net         client.FlexString `json:"net"`
		Gross       client.FlexString `json:"gross"`
		HourlyNet   client.FlexString `json:"hourly_net"`
		HourlyGross client.FlexString `json:"hourly_gross"`
	} `json:"price"`
	PriceSetup struct {
		Net   client.FlexString `json:"net"`
		Gross client.FlexString `json:"gross"`
	} `json:"price_setup"`
}

type orderPriceModel struct {
	Location     types.String `tfsdk:"location"`
	MonthlyNet   types.String `tfsdk:"monthly_net"`
	MonthlyGross types.String `tfsdk:"monthly_gross"`
	HourlyNet    types.String `tfsdk:"hourly_net"`
	HourlyGross  types.String `tfsdk:"hourly_gross"`
	SetupNet     types.String `tfsdk:"setup_net"`
	SetupGross   types.String `tfsdk:"setup_gross"`
}

func orderPrice(p apiOrderPrice) orderPriceModel {
	return orderPriceModel{
		Location:     flexStr(p.Location),
		MonthlyNet:   flexStr(p.Price.Net),
		MonthlyGross: flexStr(p.Price.Gross),
		HourlyNet:    flexStr(p.Price.HourlyNet),
		HourlyGross:  flexStr(p.Price.HourlyGross),
		SetupNet:     flexStr(p.PriceSetup.Net),
		SetupGross:   flexStr(p.PriceSetup.Gross),
	}
}

func orderPrices(in []apiOrderPrice) []orderPriceModel {
	out := make([]orderPriceModel, len(in))
	for i, p := range in {
		out[i] = orderPrice(p)
	}
	return out
}

func orderPriceDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"location":      dsStr("Location the price applies to."),
		"monthly_net":   dsStr("Monthly price without VAT."),
		"monthly_gross": dsStr("Monthly price with VAT."),
		"hourly_net":    dsStr("Hourly price without VAT; null when the product is not billed hourly."),
		"hourly_gross":  dsStr("Hourly price with VAT; null when the product is not billed hourly."),
		"setup_net":     dsStr("One-time setup fee without VAT."),
		"setup_gross":   dsStr("One-time setup fee with VAT."),
	}
}

type apiOrderableAddon struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Min    client.FlexInt  `json:"min"`
	Max    client.FlexInt  `json:"max"`
	Prices []apiOrderPrice `json:"prices"`
}

type orderableAddonModel struct {
	ID     types.String      `tfsdk:"id"`
	Name   types.String      `tfsdk:"name"`
	Min    types.Int64       `tfsdk:"min"`
	Max    types.Int64       `tfsdk:"max"`
	Prices []orderPriceModel `tfsdk:"prices"`
}

func orderableAddons(in []apiOrderableAddon) []orderableAddonModel {
	out := make([]orderableAddonModel, len(in))
	for i, a := range in {
		out[i] = orderableAddonModel{
			ID: types.StringValue(a.ID), Name: types.StringValue(a.Name),
			Min: flexInt(a.Min), Max: flexInt(a.Max), Prices: orderPrices(a.Prices),
		}
	}
	return out
}

func orderableAddonsDSAttr() dschema.ListNestedAttribute {
	return dsList("Addons that can be ordered with the server, e.g. `primary_ipv4`.", map[string]dschema.Attribute{
		"id":     dsStr("Addon id, as `addons` of the order takes it."),
		"name":   dsStr("Addon name."),
		"min":    dsInt("Minimum orderable amount."),
		"max":    dsInt("Maximum orderable amount."),
		"prices": dsList("Prices per location.", orderPriceDSAttrs()),
	})
}

// --- standard products ---

type apiServerProduct struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Description     client.StringOrList `json:"description"`
	Traffic         client.FlexString   `json:"traffic"`
	Dist            client.StringOrList `json:"dist"`
	Lang            client.StringOrList `json:"lang"`
	Location        client.StringOrList `json:"location"`
	Prices          []apiOrderPrice     `json:"prices"`
	OrderableAddons []apiOrderableAddon `json:"orderable_addons"`
}

type serverProductModel struct {
	ID              types.String          `tfsdk:"id"`
	Name            types.String          `tfsdk:"name"`
	Description     types.List            `tfsdk:"description"`
	Traffic         types.String          `tfsdk:"traffic"`
	Dists           types.List            `tfsdk:"dists"`
	Langs           types.List            `tfsdk:"langs"`
	Locations       types.List            `tfsdk:"locations"`
	Prices          []orderPriceModel     `tfsdk:"prices"`
	OrderableAddons []orderableAddonModel `tfsdk:"orderable_addons"`
}

func (m *serverProductModel) fill(p apiServerProduct) {
	m.ID = types.StringValue(p.ID)
	m.Name = types.StringValue(p.Name)
	m.Description = stringList(p.Description.Values())
	m.Traffic = flexStr(p.Traffic)
	m.Dists = stringList(p.Dist.Values())
	m.Langs = stringList(p.Lang.Values())
	m.Locations = stringList(p.Location.Values())
	m.Prices = orderPrices(p.Prices)
	m.OrderableAddons = orderableAddons(p.OrderableAddons)
}

func serverProductDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"id":               dsStr("Product id, e.g. `EX44` — the `product_id` of `hetznerrobot_server_order`."),
		"name":             dsStr("Product name."),
		"description":      dsStrList("Textual description, one line per component."),
		"traffic":          dsStr("Included traffic."),
		"dists":            dsStrList("Distributions that can be preinstalled."),
		"langs":            dsStrList("Languages of the distributions."),
		"locations":        dsStrList("Locations the product is offered in."),
		"prices":           dsList("Prices per location.", orderPriceDSAttrs()),
		"orderable_addons": orderableAddonsDSAttr(),
	}
}

// NewServerProductDataSource reads one standard server product.
func NewServerProductDataSource() datasource.DataSource { return &serverProductDataSource{} }

type serverProductDataSource struct{ dataSourceBase }

func (d *serverProductDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_product"
}

func (d *serverProductDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads one server product of the standard range — what `hetznerrobot_server_order` can order.",
		Attributes: with(serverProductDSAttrs(), map[string]dschema.Attribute{
			"id": dsRequiredStr("Product id, e.g. `EX44`."),
		}),
	}
}

func (d *serverProductDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg serverProductModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiServerProduct](ctx, d.client, "/order/server/product/"+escape(cfg.ID.ValueString()), "product", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error reading server product", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewServerProductsDataSource lists the standard server products.
func NewServerProductsDataSource() datasource.DataSource { return &serverProductsDataSource{} }

type serverProductsDataSource struct{ dataSourceBase }

type serverProductsModel struct {
	MinPrice      types.Float64        `tfsdk:"min_price"`
	MaxPrice      types.Float64        `tfsdk:"max_price"`
	MinPriceSetup types.Float64        `tfsdk:"min_price_setup"`
	MaxPriceSetup types.Float64        `tfsdk:"max_price_setup"`
	Location      types.String         `tfsdk:"location"`
	Products      []serverProductModel `tfsdk:"products"`
}

func (d *serverProductsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_products"
}

func (d *serverProductsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	optFloat := func(desc string) dschema.Float64Attribute {
		return dschema.Float64Attribute{MarkdownDescription: desc, Optional: true}
	}
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the server products of the standard range Robot currently offers, optionally filtered by price and location.",
		Attributes: map[string]dschema.Attribute{
			"min_price":       optFloat("Minimum monthly price."),
			"max_price":       optFloat("Maximum monthly price."),
			"min_price_setup": optFloat("Minimum one-time setup fee."),
			"max_price_setup": optFloat("Maximum one-time setup fee."),
			"location":        dsOptionalStr("Only products offered in this location, e.g. `FSN1`."),
			"products":        dsList("The products.", serverProductDSAttrs()),
		},
	}
}

func (d *serverProductsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg serverProductsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := url.Values{}
	for key, v := range map[string]types.Float64{
		"min_price": cfg.MinPrice, "max_price": cfg.MaxPrice,
		"min_price_setup": cfg.MinPriceSetup, "max_price_setup": cfg.MaxPriceSetup,
	} {
		if !v.IsNull() && !v.IsUnknown() {
			query.Set(key, strconv.FormatFloat(v.ValueFloat64(), 'f', -1, 64))
		}
	}
	formStr(query, "location", cfg.Location)
	list, err := client.GetList[apiServerProduct](ctx, d.client, "/order/server/product", "product", query)
	if err != nil {
		resp.Diagnostics.AddError("Error listing server products", err.Error())
		return
	}
	cfg.Products = make([]serverProductModel, len(list))
	for i, p := range list {
		cfg.Products[i].fill(p)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// --- server market ---

type apiMarketProduct struct {
	ID              client.FlexInt      `json:"id"`
	Name            string              `json:"name"`
	Description     client.StringOrList `json:"description"`
	Traffic         client.FlexString   `json:"traffic"`
	Dist            client.StringOrList `json:"dist"`
	Lang            client.StringOrList `json:"lang"`
	CPU             client.FlexString   `json:"cpu"`
	CPUBenchmark    client.FlexInt      `json:"cpu_benchmark"`
	MemorySize      client.FlexInt      `json:"memory_size"`
	HDDSize         client.FlexInt      `json:"hdd_size"`
	HDDText         client.FlexString   `json:"hdd_text"`
	HDDCount        client.FlexInt      `json:"hdd_count"`
	Datacenter      client.FlexString   `json:"datacenter"`
	NetworkSpeed    client.FlexString   `json:"network_speed"`
	Price           client.FlexString   `json:"price"`
	PriceHourly     client.FlexString   `json:"price_hourly"`
	PriceSetup      client.FlexString   `json:"price_setup"`
	PriceVAT        client.FlexString   `json:"price_vat"`
	PriceHourlyVAT  client.FlexString   `json:"price_hourly_vat"`
	PriceSetupVAT   client.FlexString   `json:"price_setup_vat"`
	FixedPrice      bool                `json:"fixed_price"`
	NextReduce      client.FlexInt      `json:"next_reduce"`
	NextReduceDate  client.FlexString   `json:"next_reduce_date"`
	OrderableAddons []apiOrderableAddon `json:"orderable_addons"`
}

type marketProductModel struct {
	ID              types.Int64           `tfsdk:"id"`
	Name            types.String          `tfsdk:"name"`
	Description     types.List            `tfsdk:"description"`
	Traffic         types.String          `tfsdk:"traffic"`
	Dists           types.List            `tfsdk:"dists"`
	Langs           types.List            `tfsdk:"langs"`
	CPU             types.String          `tfsdk:"cpu"`
	CPUBenchmark    types.Int64           `tfsdk:"cpu_benchmark"`
	MemorySize      types.Int64           `tfsdk:"memory_size"`
	HDDSize         types.Int64           `tfsdk:"hdd_size"`
	HDDText         types.String          `tfsdk:"hdd_text"`
	HDDCount        types.Int64           `tfsdk:"hdd_count"`
	Datacenter      types.String          `tfsdk:"datacenter"`
	NetworkSpeed    types.String          `tfsdk:"network_speed"`
	Price           types.String          `tfsdk:"price"`
	PriceHourly     types.String          `tfsdk:"price_hourly"`
	PriceSetup      types.String          `tfsdk:"price_setup"`
	PriceVAT        types.String          `tfsdk:"price_vat"`
	PriceHourlyVAT  types.String          `tfsdk:"price_hourly_vat"`
	PriceSetupVAT   types.String          `tfsdk:"price_setup_vat"`
	FixedPrice      types.Bool            `tfsdk:"fixed_price"`
	NextReduce      types.Int64           `tfsdk:"next_reduce"`
	NextReduceDate  types.String          `tfsdk:"next_reduce_date"`
	OrderableAddons []orderableAddonModel `tfsdk:"orderable_addons"`
}

func (m *marketProductModel) fill(p apiMarketProduct) {
	m.ID = flexInt(p.ID)
	m.Name = types.StringValue(p.Name)
	m.Description = stringList(p.Description.Values())
	m.Traffic = flexStr(p.Traffic)
	m.Dists = stringList(p.Dist.Values())
	m.Langs = stringList(p.Lang.Values())
	m.CPU = flexStr(p.CPU)
	m.CPUBenchmark = flexInt(p.CPUBenchmark)
	m.MemorySize = flexInt(p.MemorySize)
	m.HDDSize = flexInt(p.HDDSize)
	m.HDDText = flexStr(p.HDDText)
	m.HDDCount = flexInt(p.HDDCount)
	m.Datacenter = flexStr(p.Datacenter)
	m.NetworkSpeed = flexStr(p.NetworkSpeed)
	m.Price = flexStr(p.Price)
	m.PriceHourly = flexStr(p.PriceHourly)
	m.PriceSetup = flexStr(p.PriceSetup)
	m.PriceVAT = flexStr(p.PriceVAT)
	m.PriceHourlyVAT = flexStr(p.PriceHourlyVAT)
	m.PriceSetupVAT = flexStr(p.PriceSetupVAT)
	m.FixedPrice = types.BoolValue(p.FixedPrice)
	m.NextReduce = flexInt(p.NextReduce)
	m.NextReduceDate = flexStr(p.NextReduceDate)
	m.OrderableAddons = orderableAddons(p.OrderableAddons)
}

func marketProductDSAttrs() map[string]dschema.Attribute {
	return map[string]dschema.Attribute{
		"id":               dsInt("Market product id — the `product_id` of `hetznerrobot_server_market_order`."),
		"name":             dsStr("Product name, e.g. `SB109`."),
		"description":      dsStrList("Textual description, one line per component."),
		"traffic":          dsStr("Included traffic."),
		"dists":            dsStrList("Distributions that can be preinstalled."),
		"langs":            dsStrList("Languages of the distributions."),
		"cpu":              dsStr("CPU model."),
		"cpu_benchmark":    dsInt("CPU benchmark value."),
		"memory_size":      dsInt("Memory in GB."),
		"hdd_size":         dsInt("Size of one drive in GB."),
		"hdd_text":         dsStr("Drive tags, e.g. `ENT.HDD ECC INIC`."),
		"hdd_count":        dsInt("Number of drives."),
		"datacenter":       dsStr("Data center, e.g. `FSN1-DC4`."),
		"network_speed":    dsStr("Network speed, e.g. `1 Gbit/s`."),
		"price":            dsStr("Monthly price without VAT."),
		"price_hourly":     dsStr("Hourly price without VAT; null when not billed hourly."),
		"price_setup":      dsStr("One-time setup fee without VAT."),
		"price_vat":        dsStr("Monthly price with VAT."),
		"price_hourly_vat": dsStr("Hourly price with VAT; null when not billed hourly."),
		"price_setup_vat":  dsStr("One-time setup fee with VAT."),
		"fixed_price":      dsBool("Whether the price is fixed (no further reductions)."),
		"next_reduce":      dsInt("Seconds until the next price reduction."),
		"next_reduce_date": dsStr("Date of the next price reduction."),
		"orderable_addons": orderableAddonsDSAttr(),
	}
}

// NewServerMarketProductDataSource reads one server market offer.
func NewServerMarketProductDataSource() datasource.DataSource {
	return &serverMarketProductDataSource{}
}

type serverMarketProductDataSource struct{ dataSourceBase }

func (d *serverMarketProductDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_market_product"
}

func (d *serverMarketProductDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads one offer of the server market — what `hetznerrobot_server_market_order` can order. " +
			"Market offers come and go; reading one that is gone fails.",
		Attributes: with(marketProductDSAttrs(), map[string]dschema.Attribute{
			"id": dsRequiredInt("Market product id."),
		}),
	}
}

func (d *serverMarketProductDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg marketProductModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := client.GetObject[apiMarketProduct](ctx, d.client, fmt.Sprintf("/order/server_market/product/%d", cfg.ID.ValueInt64()), "product", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error reading server market product", err.Error())
		return
	}
	cfg.fill(got)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// NewServerMarketProductsDataSource lists the server market.
func NewServerMarketProductsDataSource() datasource.DataSource {
	return &serverMarketProductsDataSource{}
}

type serverMarketProductsDataSource struct{ dataSourceBase }

type marketProductsModel struct {
	Products []marketProductModel `tfsdk:"products"`
}

func (d *serverMarketProductsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_market_products"
}

func (d *serverMarketProductsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists every offer of the server market. Filter it in HCL, e.g. with a `for` expression over `memory_size` or `price`.",
		Attributes: map[string]dschema.Attribute{
			"products": dsList("The market offers.", marketProductDSAttrs()),
		},
	}
}

func (d *serverMarketProductsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	list, err := client.GetList[apiMarketProduct](ctx, d.client, "/order/server_market/product", "product", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error listing server market products", err.Error())
		return
	}
	out := marketProductsModel{Products: make([]marketProductModel, len(list))}
	for i, p := range list {
		out.Products[i].fill(p)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &out)...)
}

// --- server addons ---

type apiAddonProduct struct {
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Type  client.FlexString `json:"type"`
	Price apiOrderPrice     `json:"price"`
}

type addonProductModel struct {
	ID    types.String    `tfsdk:"id"`
	Name  types.String    `tfsdk:"name"`
	Type  types.String    `tfsdk:"type"`
	Price orderPriceModel `tfsdk:"price"`
}

// NewServerAddonProductsDataSource lists the addons a server can order.
func NewServerAddonProductsDataSource() datasource.DataSource {
	return &serverAddonProductsDataSource{}
}

type serverAddonProductsDataSource struct{ dataSourceBase }

type serverAddonProductsModel struct {
	ServerNumber types.Int64         `tfsdk:"server_number"`
	Products     []addonProductModel `tfsdk:"products"`
}

func (d *serverAddonProductsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_addon_products"
}

func (d *serverAddonProductsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Lists the addons — additional IPs, subnets, failover subnets — that `hetznerrobot_server_addon_order` can order for a server.",
		Attributes: map[string]dschema.Attribute{
			"server_number": dsRequiredInt("Server to list the addons for."),
			"products": dsList("The addon products.", map[string]dschema.Attribute{
				"id":    dsStr("Addon product id, e.g. `additional_ipv4`."),
				"name":  dsStr("Addon name."),
				"type":  dsStr("Addon type, e.g. `ip_ipv4`, `subnet_ipv4` or `failover_subnet_ipv4`."),
				"price": dsObject("Price of the addon in the server's location.", orderPriceDSAttrs()),
			}),
		},
	}
}

func (d *serverAddonProductsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg serverAddonProductsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	list, err := client.GetList[apiAddonProduct](ctx, d.client, fmt.Sprintf("/order/server_addon/%d/product", cfg.ServerNumber.ValueInt64()), "product", nil)
	if err != nil {
		resp.Diagnostics.AddError("Error listing server addon products", err.Error())
		return
	}
	cfg.Products = make([]addonProductModel, len(list))
	for i, p := range list {
		cfg.Products[i] = addonProductModel{
			ID: types.StringValue(p.ID), Name: types.StringValue(p.Name), Type: flexStr(p.Type), Price: orderPrice(p.Price),
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// --- currency ---

// NewCurrencyDataSource reads the account's currency.
func NewCurrencyDataSource() datasource.DataSource { return &currencyDataSource{} }

type currencyDataSource struct{ dataSourceBase }

type currencyModel struct {
	Currency types.String `tfsdk:"currency"`
}

func (d *currencyDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_currency"
}

func (d *currencyDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Reads the currency of the account — the one every price of the product data sources is in.",
		Attributes: map[string]dschema.Attribute{
			"currency": dsStr("Currency code, e.g. `EUR`."),
		},
	}
}

func (d *currencyDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	var got struct {
		Currency string `json:"currency"`
	}
	if err := d.client.Get(ctx, "/order/currency", nil, &got); err != nil {
		resp.Diagnostics.AddError("Error reading currency", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &currencyModel{Currency: types.StringValue(got.Currency)})...)
}
