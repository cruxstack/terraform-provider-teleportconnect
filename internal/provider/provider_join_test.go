package provider

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccProviderJoinGitHub validates the github delegated join end-to-end.
// It can only run inside an actual GitHub Actions job (which exposes the OIDC
// token endpoint) against a cluster that has a matching join token, so it is
// opt-in: set TC_JOIN_ACCTEST=true and provide TC_PROXY_ADDRESS +
// TC_GITHUB_JOIN_TOKEN. It is skipped in normal PR CI (which uses the local
// docker-compose cluster that has no GitHub OIDC trust).
func TestAccProviderJoinGitHub(t *testing.T) {
	if os.Getenv("TC_JOIN_ACCTEST") != "true" {
		t.Skip("github join acctest is opt-in; set TC_JOIN_ACCTEST=true to run inside a GitHub Actions job")
	}
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("github join acctest must run inside GitHub Actions (needs the OIDC token endpoint)")
	}
	proxy := os.Getenv("TC_PROXY_ADDRESS")
	token := os.Getenv("TC_GITHUB_JOIN_TOKEN")
	if proxy == "" || token == "" {
		t.Skip("TC_PROXY_ADDRESS and TC_GITHUB_JOIN_TOKEN are required")
	}

	dbName := os.Getenv("TC_DATABASE_NAME")
	if dbName == "" {
		t.Skip("TC_DATABASE_NAME required to exercise a data source after joining")
	}

	insecure := ""
	if os.Getenv("TC_INSECURE") == "true" {
		insecure = "  insecure = true\n"
	}
	audience := ""
	if a := os.Getenv("TC_JOIN_AUDIENCE"); a != "" {
		audience = fmt.Sprintf("  join_audience = %q\n", a)
	}

	config := fmt.Sprintf(`
provider "teleportconnect" {
  proxy_address = %q
  join_method   = "github"
  join_token    = %q
%s%s}

data "teleportconnect_cluster" "test" {}

data "teleportconnect_database" "test" {
  name = %q
}
`, proxy, token, insecure, audience, dbName)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.teleportconnect_cluster.test", "cluster_name"),
					resource.TestCheckResourceAttr("data.teleportconnect_database.test", "matched_name", dbName),
				),
			},
		},
	})
}
