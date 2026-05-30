---
page_title: "Teleport RBAC"
subcategory: ""
---

# Teleport RBAC

The identity used by the provider needs a Teleport role granting it enough
access to discover resources and issue short-lived certificates.

## Minimum permissions

| Capability | Why |
| --- | --- |
| Read/list `db_server` | `data.teleportconnect_database` and database protocol lookup. |
| Read/list `node` | `data.teleportconnect_node`. |
| `db_users` / `db_names` | Issue database certificates for the requested user/db. |
| `logins` | Issue SSH certificates for the requested OS login. |
| `db_labels` / `node_labels` | Scope which databases/nodes are reachable. |

## Example role

```yaml
kind: role
version: v7
metadata:
  name: terraform-teleportconnect
spec:
  allow:
    # Restrict to the databases/nodes this automation should reach.
    db_labels:
      env: ["prod"]
    db_users: ["readonly"]
    db_names: ["appdb"]

    node_labels:
      role: ["bastion"]
    logins: ["ec2-user"]

    rules:
      - resources: [db_server, node]
        verbs: [read, list]
```

Attach the role to the user or bot whose identity the provider consumes:

```sh
tctl users add terraform --roles terraform-teleportconnect
# or, for Machine ID:
tctl bots add terraform --roles terraform-teleportconnect
```

## Certificate lifetimes

Certificates issued by the credential and tunnel resources default to a
1-hour TTL and can be tuned per-resource with the `ttl` attribute. The
provider requests **ECDSA P-256** keys, which require a Teleport auth server
running **v15 or newer**.

## Least privilege

Prefer narrowly scoped `db_labels`/`node_labels`, `db_users`, `db_names`, and
`logins` so the automation identity can only reach the specific resources it
needs. Avoid wildcards (`'*'`) outside of local testing.
