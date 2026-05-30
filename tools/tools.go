//go:build tools

// Package tools tracks build-time tool dependencies so they are recorded in
// go.mod and can be installed with a pinned version. It is never compiled
// into the provider binary (guarded by the "tools" build tag).
package tools

import (
	// Documentation generation for the Terraform Registry.
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)
