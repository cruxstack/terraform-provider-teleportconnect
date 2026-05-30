package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccDataCluster_basic(t *testing.T) {
	config := testProviderConfig() + `
data "teleportconnect_cluster" "test" {}
`

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.teleportconnect_cluster.test", "cluster_name"),
					resource.TestCheckResourceAttrPair(
						"data.teleportconnect_cluster.test", "id",
						"data.teleportconnect_cluster.test", "cluster_name",
					),
					resource.TestCheckResourceAttrSet("data.teleportconnect_cluster.test", "server_version"),
					resource.TestMatchResourceAttr("data.teleportconnect_cluster.test", "ca_certificate", regexp.MustCompile(`BEGIN CERTIFICATE`)),
				),
			},
		},
	})
}
