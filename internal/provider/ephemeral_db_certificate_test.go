package provider

import (
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/echoprovider"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// testAccProtoV6WithEcho adds the echo provider so ephemeral resource output
// can be captured into a managed resource for assertions.
var testAccProtoV6WithEcho = map[string]func() (tfprotov6.ProviderServer, error){
	"teleportconnect": testAccProtoV6ProviderFactories["teleportconnect"],
	"echo":            echoprovider.NewProviderServer(),
}

func TestAccEphemeralDBCertificate_basic(t *testing.T) {
	dbName := os.Getenv("TC_DATABASE_NAME")
	if dbName == "" {
		t.Skip("TC_DATABASE_NAME not set; skipping")
	}
	dbUser := os.Getenv("TC_DATABASE_USER")

	config := testProviderConfig() + fmt.Sprintf(`
ephemeral "teleportconnect_db_certificate" "test" {
  database = %q
  db_user  = %q
  db_name  = "postgres"
}

provider "echo" {
  data = ephemeral.teleportconnect_db_certificate.test
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
					resource.TestCheckResourceAttrSet("echo.test", "data.host"),
					resource.TestCheckResourceAttrSet("echo.test", "data.port"),
					resource.TestMatchResourceAttr("echo.test", "data.certificate", regexp.MustCompile(`BEGIN CERTIFICATE`)),
					resource.TestMatchResourceAttr("echo.test", "data.private_key", regexp.MustCompile(`BEGIN PRIVATE KEY`)),
					resource.TestMatchResourceAttr("echo.test", "data.ca_certificate", regexp.MustCompile(`BEGIN CERTIFICATE`)),
				),
			},
		},
	})
}
