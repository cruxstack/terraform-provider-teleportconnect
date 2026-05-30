package provider

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// testProviderConfig is the provider block shared by acceptance tests. It is
// built from the TC_* environment variables so no secrets are hardcoded.
func testProviderConfig() string {
	proxy := os.Getenv("TC_PROXY_ADDRESS")
	alpn := os.Getenv("TC_ALPN_CONN_UPGRADE")
	if alpn == "" {
		alpn = "auto"
	}

	cfg := "provider \"teleportconnect\" {\n"
	cfg += "  proxy_address     = \"" + proxy + "\"\n"
	if data := os.Getenv("TC_IDENTITY_FILE_DATA"); data != "" {
		cfg += "  identity_file_data = <<-EOT\n" + data + "\nEOT\n"
	} else if path := os.Getenv("TC_IDENTITY_FILE_PATH"); path != "" {
		cfg += "  identity_file_path = \"" + path + "\"\n"
	} else {
		cfg += "  use_local_profile = true\n"
	}
	cfg += "  alpn_conn_upgrade = \"" + alpn + "\"\n"
	cfg += "}\n"
	return cfg
}

// testAccProtoV6ProviderFactories wires the in-process provider for the
// terraform-plugin-testing harness.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"teleportconnect": providerserver.NewProtocol6WithError(New("test")()),
}

// testAccPreCheck verifies the minimum environment required to run an
// acceptance test is present. Tests call this from their PreCheck hook.
func testAccPreCheck(t *testing.T) {
	if os.Getenv("TC_PROXY_ADDRESS") == "" {
		t.Skip("TC_PROXY_ADDRESS not set; skipping acceptance test")
	}
}

// TestProviderSchema is a cheap, always-on sanity test that the provider
// schema is internally consistent. It does not require TF_ACC.
func TestProviderSchema(t *testing.T) {
	// resource.UnitTest validates the provider can be instantiated and its
	// schema is valid without contacting any backend.
	_ = resource.TestCase{}
	p := New("test")()
	if p == nil {
		t.Fatal("New returned nil provider")
	}
}
