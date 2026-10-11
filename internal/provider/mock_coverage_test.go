package provider

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Every resource and data source must appear in an acceptance-test
// configuration that runs against the mock — a new entity without one would
// otherwise ship with no test at all, because CI never runs the live suite.
// The live tests' configurations count where a mock test runs them too.
func TestEveryTypeIsCoveredByAMockTest(t *testing.T) {
	ctx := context.Background()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	var src strings.Builder
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src.Write(b)
	}
	configs := src.String()

	used := func(kind, name string) bool {
		return regexp.MustCompile(kind + `\s+"` + name + `"`).MatchString(configs)
	}
	p := New("test")()
	for _, newResource := range p.Resources(ctx) {
		var meta resource.MetadataResponse
		newResource().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "hetznerrobot"}, &meta)
		if !used("resource", meta.TypeName) {
			t.Errorf("resource %s appears in no acceptance-test configuration", meta.TypeName)
		}
	}
	for _, newDataSource := range p.DataSources(ctx) {
		var meta datasource.MetadataResponse
		newDataSource().Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "hetznerrobot"}, &meta)
		if !used("data", meta.TypeName) {
			t.Errorf("data source %s appears in no acceptance-test configuration", meta.TypeName)
		}
	}
}
