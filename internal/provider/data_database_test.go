package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccDataDatabase_byName(t *testing.T) {
	dbName := os.Getenv("TC_DATABASE_NAME")
	if dbName == "" {
		t.Skip("TC_DATABASE_NAME not set; skipping")
	}

	config := testProviderConfig() + fmt.Sprintf(`
data "teleportconnect_database" "test" {
  name = %q
}
`, dbName)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.teleportconnect_database.test", "matched_name", dbName),
					resource.TestCheckResourceAttr("data.teleportconnect_database.test", "id", dbName),
					resource.TestCheckResourceAttrSet("data.teleportconnect_database.test", "protocol"),
				),
			},
		},
	})
}

func TestAccDataDatabase_byLabels(t *testing.T) {
	labelKey := os.Getenv("TC_DATABASE_LABEL_KEY")
	labelVal := os.Getenv("TC_DATABASE_LABEL_VALUE")
	if labelKey == "" || labelVal == "" {
		t.Skip("TC_DATABASE_LABEL_KEY/VALUE not set; skipping")
	}

	config := testProviderConfig() + fmt.Sprintf(`
data "teleportconnect_database" "test" {
  labels = {
    %q = %q
  }
}
`, labelKey, labelVal)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.teleportconnect_database.test", "matched_name"),
					resource.TestCheckResourceAttr("data.teleportconnect_database.test", "all_labels."+labelKey, labelVal),
				),
			},
		},
	})
}
