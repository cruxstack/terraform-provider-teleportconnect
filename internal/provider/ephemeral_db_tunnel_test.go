package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

func TestAccEphemeralDBTunnel_basic(t *testing.T) {
	dbName := os.Getenv("TC_DATABASE_NAME")
	if dbName == "" {
		t.Skip("TC_DATABASE_NAME not set; skipping")
	}
	dbUser := os.Getenv("TC_DATABASE_USER")

	config := testProviderConfig() + fmt.Sprintf(`
ephemeral "teleportconnect_db_tunnel" "test" {
  database = %q
  db_user  = %q
  db_name  = "postgres"
}

provider "echo" {
  data = ephemeral.teleportconnect_db_tunnel.test
}

resource "echo" "test" {}
`, dbName, dbUser)

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
