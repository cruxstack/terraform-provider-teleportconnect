---
page_title: "ALPN connection upgrade"
subcategory: ""
---

# ALPN connection upgrade

Teleport uses TLS routing (ALPN) to multiplex database, SSH, and other
protocols over a single proxy port. When the proxy is reachable directly,
the client negotiates the appropriate ALPN protocol value during the TLS
handshake and the proxy routes accordingly.

## The L7 load balancer problem

When the Teleport proxy sits behind a Layer 7 load balancer that terminates
TLS with its own certificate (for example an AWS Application Load Balancer
fronting the proxy with an ACM/Let's Encrypt certificate), the load balancer
may negotiate ALPN values back to the client while terminating TLS itself.
This fools direct TLS routing: the client thinks it is talking to the proxy,
but the LB has already terminated the connection.

To work around this, Teleport supports an **HTTPS connection upgrade**: the
client first performs a plain HTTPS request that asks the proxy to upgrade
the connection to an ALPN-routed tunnel, then runs the protocol over that
upgraded connection.

## The `alpn_conn_upgrade` option

The upstream automatic probe (`client.IsALPNConnUpgradeRequired`) is
documented as unreliable for many real-world load balancers, so this provider
exposes an explicit override:

| Value | Behavior |
| --- | --- |
| `auto` (default) | Probe the proxy and decide automatically. |
| `yes` | Always perform the HTTPS connection upgrade. |
| `no` | Never upgrade; use direct TLS routing. |

```hcl
provider "teleportconnect" {
  proxy_address     = "teleport.example.com:443"
  use_local_profile = true

  # The proxy is behind an AWS ALB that terminates TLS with a public cert,
  # so force the upgrade rather than relying on the unreliable auto-probe.
  alpn_conn_upgrade = "yes"
}
```

## Recommendation

If you know your proxy is fronted by an L7 load balancer, set
`alpn_conn_upgrade = "yes"` explicitly. If you connect directly to the proxy
(no L7 LB), `auto` or `no` both work; `no` avoids an extra round trip.

~> **Note**: For SSH tunnels, the `auto` setting does not probe (the
underlying proxy client has no auto-detect helper); it behaves like `no`. Set
`yes` explicitly for SSH tunnels through an L7-LB-fronted proxy.
