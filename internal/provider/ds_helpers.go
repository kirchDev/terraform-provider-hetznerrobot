package provider

import (
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Data sources reuse the resource models where they can, so their attribute
// maps mirror the resource schemas with every attribute Computed. The
// constructors below keep those maps short.

func dsStr(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
}

func dsInt(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: desc, Computed: true}
}

func dsFloat(desc string) schema.Float64Attribute {
	return schema.Float64Attribute{MarkdownDescription: desc, Computed: true}
}

func dsBool(desc string) schema.BoolAttribute {
	return schema.BoolAttribute{MarkdownDescription: desc, Computed: true}
}

func dsStrList(desc string) schema.ListAttribute {
	return schema.ListAttribute{MarkdownDescription: desc, Computed: true, ElementType: types.StringType}
}

func dsStrMap(desc string) schema.MapAttribute {
	return schema.MapAttribute{MarkdownDescription: desc, Computed: true, ElementType: types.StringType}
}

func dsRequiredStr(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Required: true}
}

func dsRequiredInt(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: desc, Required: true}
}

func dsOptionalStr(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Optional: true}
}

// dsLookupStr/dsLookupInt are lookup keys of a single-entity data source that
// may be given or computed — "by id or by name".
func dsLookupStr(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Optional: true, Computed: true}
}

func dsLookupInt(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: desc, Optional: true, Computed: true}
}

// dsList wraps an entity's attribute map as a computed list of objects.
func dsList(desc string, attrs map[string]schema.Attribute) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		MarkdownDescription: desc,
		Computed:            true,
		NestedObject:        schema.NestedAttributeObject{Attributes: attrs},
	}
}

// dsObject wraps an attribute map as a computed nested object.
func dsObject(desc string, attrs map[string]schema.Attribute) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{MarkdownDescription: desc, Computed: true, Attributes: attrs}
}

// with returns a copy of attrs with the given attributes replaced or added — a
// single-entity data source turns the computed key into its required lookup.
func with(attrs map[string]schema.Attribute, over map[string]schema.Attribute) map[string]schema.Attribute {
	out := make(map[string]schema.Attribute, len(attrs)+len(over))
	for k, v := range attrs {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}
