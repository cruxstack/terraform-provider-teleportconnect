package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestAccEphemeralSSHTunnel_basic(t *testing.T) {
	gateway := os.Getenv("TC_SSH_GATEWAY_NODE")
	login := os.Getenv("TC_SSH_LOGIN")
	targetHost := os.Getenv("TC_SSH_TARGET_HOST")
	targetPort := os.Getenv("TC_SSH_TARGET_PORT")
	if gateway == "" || login == "" || targetHost == "" || targetPort == "" {
		t.Skip("TC_SSH_* env vars not set; skipping")
	}

	config := testProviderConfig() + fmt.Sprintf(`
ephemeral "teleportconnect_ssh_tunnel" "test" {
  gateway_node = %q
  ssh_login    = %q
  target_host  = %q
  target_port  = %s
}

provider "echo" {
  data = ephemeral.teleportconnect_ssh_tunnel.test
}

resource "echo" "test" {}
`, gateway, login, targetHost, targetPort)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_10_0)},
		ProtoV6ProviderFactories: testAccProtoV6WithEcho,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("echo.test", "data.local_host", "127.0.0.1"),
					resource.TestMatchResourceAttr("echo.test", "data.local_port", regexp.MustCompile(`^\d+$`)),
				),
			},
		},
	})
}
