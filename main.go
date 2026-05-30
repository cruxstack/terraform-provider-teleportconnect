package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/cruxstack/terraform-provider-teleportconnect/internal/provider"
)

// Generate the Terraform Registry documentation from the provider schema, the
// examples/ directory, and the hand-written guides under templates/guides/.
// Run with `make docs`.
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name teleportconnect

// version is set at build time via -ldflags. The default "dev" suffix marks
// non-release builds (used by dev_overrides during prototyping).
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/cruxstack/teleportconnect",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
