package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccDataNode_byHostname(t *testing.T) {
	hostname := os.Getenv("TC_NODE_HOSTNAME")
	if hostname == "" {
		t.Skip("TC_NODE_HOSTNAME not set; skipping")
	}

	config := testProviderConfig() + fmt.Sprintf(`
data "teleportconnect_node" "test" {
  hostname = %q
}
`, hostname)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.teleportconnect_node.test", "matched_hostname", hostname),
					resource.TestCheckResourceAttrSet("data.teleportconnect_node.test", "matched_name"),
					resource.TestCheckResourceAttrSet("data.teleportconnect_node.test", "addr"),
				),
			},
		},
	})
}

func TestAccDataNode_byLabels(t *testing.T) {
	labelKey := os.Getenv("TC_NODE_LABEL_KEY")
	labelVal := os.Getenv("TC_NODE_LABEL_VALUE")
	if labelKey == "" || labelVal == "" {
		t.Skip("TC_NODE_LABEL_KEY/VALUE not set; skipping")
	}

	config := testProviderConfig() + fmt.Sprintf(`
data "teleportconnect_node" "test" {
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
					resource.TestCheckResourceAttrSet("data.teleportconnect_node.test", "matched_hostname"),
					resource.TestCheckResourceAttr("data.teleportconnect_node.test", "all_labels."+labelKey, labelVal),
				),
			},
		},
	})
}
