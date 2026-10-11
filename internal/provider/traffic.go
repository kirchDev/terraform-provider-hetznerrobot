package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/kirchDev/terraform-provider-hetznerrobot/internal/client"
)

var (
	_ datasource.DataSource                   = (*trafficDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*trafficDataSource)(nil)
)

// trafficFormats are the interval syntaxes per query type, as documented:
// day "2010-09-01T00", month "2010-09-01", year "2010-09".
var trafficFormats = map[string]struct {
	re      *regexp.Regexp
	example string
}{
	"day":   {regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}$`), "2026-10-01T00"},
	"month": {regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`), "2026-10-01"},
	"year":  {regexp.MustCompile(`^\d{4}-\d{2}$`), "2026-10"},
}

// NewTrafficDataSource queries the traffic of IPs and subnets.
func NewTrafficDataSource() datasource.DataSource { return &trafficDataSource{} }

type trafficDataSource struct{ dataSourceBase }

type apiTrafficVolume struct {
	In  client.FlexFloat `json:"in"`
	Out client.FlexFloat `json:"out"`
	Sum client.FlexFloat `json:"sum"`
}

type trafficEntryModel struct {
	Address  types.String  `tfsdk:"address"`
	Interval types.String  `tfsdk:"interval"`
	In       types.Float64 `tfsdk:"in"`
	Out      types.Float64 `tfsdk:"out"`
	Sum      types.Float64 `tfsdk:"sum"`
}

type trafficModel struct {
	Type         types.String        `tfsdk:"type"`
	From         types.String        `tfsdk:"from"`
	To           types.String        `tfsdk:"to"`
	IPs          types.List          `tfsdk:"ips"`
	Subnets      types.List          `tfsdk:"subnets"`
	SingleValues types.Bool          `tfsdk:"single_values"`
	Entries      []trafficEntryModel `tfsdk:"entries"`
}

func (d *trafficDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_traffic"
}

func (d *trafficDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = dschema.Schema{
		MarkdownDescription: "Queries the traffic of IP addresses and subnets over an interval, in GB. " +
			"`day` sums hours within a day, `month` days within a month, `year` months within a year; " +
			"data exists only once the hour, day or month has passed. " +
			"Addresses without traffic data are left out of `entries` by Robot.",
		Attributes: map[string]dschema.Attribute{
			"type": dschema.StringAttribute{
				MarkdownDescription: "Query type: `day`, `month` or `year`.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf("day", "month", "year")},
			},
			"from": dsRequiredStr("Start of the interval: `YYYY-MM-DDTHH` for `day`, `YYYY-MM-DD` for `month`, `YYYY-MM` for `year`."),
			"to":   dsRequiredStr("End of the interval, in the same format as `from`."),
			"ips": dschema.ListAttribute{
				MarkdownDescription: "IP addresses to query. Set `ips`, `subnets` or both.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"subnets": dschema.ListAttribute{
				MarkdownDescription: "Subnet addresses to query (the network address, e.g. `2a01:4f8:111:4221::`).",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"single_values": dschema.BoolAttribute{
				MarkdownDescription: "Group the data by hour (`day`), day (`month`) or month (`year`) instead of one sum per address.",
				Optional:            true,
			},
			"entries": dsList("Traffic per address — and per interval with `single_values`, sorted by address and interval.", map[string]dschema.Attribute{
				"address":  dsStr("IP address, or subnet in CIDR notation."),
				"interval": dsStr("Hour, day or month of the value (`01`, `02`, …) with `single_values`; null otherwise."),
				"in":       dsFloat("Incoming traffic in GB."),
				"out":      dsFloat("Outgoing traffic in GB."),
				"sum":      dsFloat("Total traffic in GB."),
			}),
		},
	}
}

func (d *trafficDataSource) ValidateConfig(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var cfg trafficModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.IPs.IsNull() && cfg.Subnets.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("ips"), "Nothing to query", "Set `ips`, `subnets` or both.")
	}
	if cfg.Type.IsNull() || cfg.Type.IsUnknown() {
		return
	}
	f, ok := trafficFormats[cfg.Type.ValueString()]
	if !ok {
		return // the OneOf validator reports it
	}
	for name, v := range map[string]types.String{"from": cfg.From, "to": cfg.To} {
		if v.IsNull() || v.IsUnknown() || f.re.MatchString(v.ValueString()) {
			continue
		}
		resp.Diagnostics.AddAttributeError(path.Root(name), "Invalid interval",
			fmt.Sprintf("A %s query takes %s like %q, got %q.", cfg.Type.ValueString(), name, f.example, v.ValueString()))
	}
}

func (d *trafficDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg trafficModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	form := url.Values{}
	formStr(form, "type", cfg.Type)
	formStr(form, "from", cfg.From)
	formStr(form, "to", cfg.To)
	formArray(form, "ip", valueStrings(ctx, cfg.IPs, &resp.Diagnostics))
	formArray(form, "subnet", valueStrings(ctx, cfg.Subnets, &resp.Diagnostics))
	single := cfg.SingleValues.ValueBool()
	if single {
		form.Set("single_values", "true")
	}

	// POST, but a read: the query is too long for a URL.
	var got struct {
		Traffic struct {
			Data json.RawMessage `json:"data"`
		} `json:"traffic"`
	}
	if err := d.client.Send(ctx, http.MethodPost, "/traffic", form, &got); err != nil {
		resp.Diagnostics.AddError("Error querying traffic", err.Error())
		return
	}
	entries, err := trafficEntries(got.Traffic.Data, single)
	if err != nil {
		resp.Diagnostics.AddError("Error decoding traffic", err.Error())
		return
	}
	cfg.Entries = entries
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// trafficEntries flattens Robot's {address: {in, out, sum}} or, with
// single_values, {address: {interval: {in, out, sum}}} into sorted rows. Robot
// renders an empty object as [] (PHP), which reads as no data.
func trafficEntries(raw json.RawMessage, single bool) ([]trafficEntryModel, error) {
	out := []trafficEntryModel{}
	if trafficEmpty(raw) {
		return out, nil
	}
	var byAddress map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byAddress); err != nil {
		return nil, err
	}
	row := func(addr string, interval types.String, t apiTrafficVolume) trafficEntryModel {
		return trafficEntryModel{Address: types.StringValue(addr), Interval: interval,
			In: flexFloat(t.In), Out: flexFloat(t.Out), Sum: flexFloat(t.Sum)}
	}
	for addr, v := range byAddress {
		if !single {
			var t apiTrafficVolume
			if err := json.Unmarshal(v, &t); err != nil {
				return nil, err
			}
			out = append(out, row(addr, types.StringNull(), t))
			continue
		}
		if trafficEmpty(v) {
			continue
		}
		var byInterval map[string]apiTrafficVolume
		if err := json.Unmarshal(v, &byInterval); err != nil {
			return nil, err
		}
		for interval, t := range byInterval {
			out = append(out, row(addr, types.StringValue(interval), t))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].Address.ValueString(), out[j].Address.ValueString(); a != b {
			return a < b
		}
		return out[i].Interval.ValueString() < out[j].Interval.ValueString()
	})
	return out, nil
}

func trafficEmpty(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || string(t) == "null" || t[0] == '['
}
