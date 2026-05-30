# Local smoke test

A manual, end-to-end smoke test that drives a real Teleport-protected database
through both `teleportconnect_db_credentials` and `teleportconnect_db_tunnel`,
then runs `psql` to confirm connectivity.

This is intentionally kept out of `examples/` because it depends on `psql` and a
reachable database; it is for developer verification only.

## Usage

```sh
# Build and point a dev_overrides .terraformrc at the build dir:
make build   # from repo root -> ./build/terraform-provider-teleportconnect

cat > .terraformrc <<EOF
provider_installation {
  dev_overrides {
    "cruxstack/teleportconnect" = "$(cd ../.. && pwd)/build"
  }
  direct {}
}
EOF

export TF_CLI_CONFIG_FILE=$(pwd)/.terraformrc
export TF_VAR_proxy_address="teleport.example.com:443"
export TF_VAR_database_name="mycorp-postgres"
export TF_VAR_db_user="readonly"
# export TF_VAR_alpn_conn_upgrade="yes"   # if behind an L7 LB

tsh login --proxy "$TF_VAR_proxy_address"
terraform apply
```

Outputs are written to `/tmp/teleportconnect-smoke/`.
