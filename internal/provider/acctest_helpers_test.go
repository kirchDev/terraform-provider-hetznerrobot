package provider

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// testAccProtoV6ProviderFactories wires the in-process provider for the
// terraform-plugin-testing harness.
func testAccProtoV6ProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"hetznerrobot": providerserver.NewProtocol6WithError(New("test")()),
	}
}

// accEnv prepares the acceptance environment shared by the mock and the live
// tests.
func accEnv(t *testing.T) {
	t.Helper()
	// The harness reattaches the provider under the address tofu resolves a bare
	// "hetznerrobot" to (registry.opentofu.org/hashicorp/...); the framework
	// otherwise defaults to registry.terraform.io/-/..., which tofu rejects.
	t.Setenv("TF_ACC_PROVIDER_HOST", "registry.opentofu.org")
	t.Setenv("TF_ACC_PROVIDER_NAMESPACE", "hashicorp")

	// Let `TF_ACC=1 go test` find a TF binary without the caller exporting a
	// path: prefer an explicit one, else fall back to tofu on PATH.
	if os.Getenv("TF_ACC_TERRAFORM_PATH") == "" {
		if tofu, err := exec.LookPath("tofu"); err == nil {
			t.Setenv("TF_ACC_TERRAFORM_PATH", tofu)
		}
	}
}

// terraformState shortens the state type in CheckDestroy functions.
type terraformState = terraform.State

// stateAttr builds an import id from an attribute of a resource in state —
// for ids only Robot knows (fingerprints, generated numbers).
func stateAttr(addr, attr string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[addr]
		if !ok {
			return "", fmt.Errorf("%s not in state", addr)
		}
		return rs.Primary.Attributes[attr], nil
	}
}

// stateAttrs joins several attributes of a resource with "/" into an import id.
func stateAttrs(addr string, attrs ...string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[addr]
		if !ok {
			return "", fmt.Errorf("%s not in state", addr)
		}
		parts := make([]string, len(attrs))
		for i, a := range attrs {
			parts[i] = rs.Primary.Attributes[a]
		}
		return strings.Join(parts, "/"), nil
	}
}
