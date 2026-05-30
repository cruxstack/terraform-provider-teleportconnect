// Package dbcerts wraps the Teleport API calls needed to issue a short-lived
// database client certificate plus the cluster TLS CA bundle.
//
// This is the in-process equivalent of `tsh db login` + `tsh db config`.
// It's used both by the teleport_access_db_credentials ephemeral resource
// (which surfaces the PEM material to downstream providers) and by
// teleport_access_db_tunnel (which feeds the same material into a local
// ALPN proxy).
package dbcerts

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/gravitational/teleport/api/client"
	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/defaults"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Request describes a single database certificate issuance.
type Request struct {
	// Database is the Teleport database service name (matches `tsh db ls`).
	Database string
	// Protocol is the database protocol (e.g. "postgres"). When empty,
	// Issue looks it up from the Teleport database resource.
	Protocol string
	// DBUser is the database user to embed in the cert.
	DBUser string
	// DBName is the database name to embed in the cert.
	DBName string
	// TTL is how long the cert should be valid. Zero means 1h.
	TTL time.Duration
	// RouteToCluster overrides the route_to_cluster field. Empty means
	// "let the auth server pick the cluster the proxy belongs to".
	RouteToCluster string
}

// Result is the material returned by Issue.
type Result struct {
	// CertPEM is the PEM-encoded TLS client certificate.
	CertPEM []byte
	// KeyPEM is the PEM-encoded ECDSA P-256 private key (PKCS#8).
	KeyPEM []byte
	// CAPEM is the PEM-encoded cluster TLS CA bundle.
	CAPEM []byte
	// Protocol is the resolved protocol (useful when Request.Protocol was empty).
	Protocol string
}

// Issue generates a fresh keypair, asks the Teleport auth service to sign a
// database-scoped user certificate, and returns the certs material plus the
// cluster TLS CA bundle.
func Issue(ctx context.Context, c *client.Client, req Request) (*Result, error) {
	if c == nil {
		return nil, errors.New("teleport client is nil")
	}
	if req.Database == "" {
		return nil, errors.New("Request.Database is required")
	}

	ttl := req.TTL
	if ttl <= 0 {
		ttl = time.Hour
	}

	protocol := req.Protocol
	if protocol == "" {
		var err error
		protocol, err = lookupProtocol(ctx, c, req.Database)
		if err != nil {
			return nil, err
		}
	}

	tpUser, err := c.GetCurrentUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching current user: %w", err)
	}

	tflog.Debug(ctx, "generating ecdsa keypair for database certificate", map[string]any{
		"database": req.Database,
		"protocol": protocol,
		"user":     tpUser.GetName(),
	})

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating private key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("marshaling public key: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})

	certs, err := c.GenerateUserCerts(ctx, proto.UserCertsRequest{
		Username:       tpUser.GetName(),
		TLSPublicKey:   pubPEM,
		Expires:        time.Now().Add(ttl),
		Usage:          proto.UserCertsRequest_Database,
		RouteToCluster: req.RouteToCluster,
		RouteToDatabase: proto.RouteToDatabase{
			ServiceName: req.Database,
			Protocol:    protocol,
			Username:    req.DBUser,
			Database:    req.DBName,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("issuing database certificate: %w", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("marshaling private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: keyDER,
	})

	caResp, err := c.GetClusterCACert(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching cluster CA: %w", err)
	}
	caPEM := caResp.TLSCA
	if !bytes.HasSuffix(caPEM, []byte("\n")) {
		caPEM = append(caPEM, '\n')
	}

	return &Result{
		CertPEM:  certs.TLS,
		KeyPEM:   keyPEM,
		CAPEM:    caPEM,
		Protocol: protocol,
	}, nil
}

// lookupProtocol asks the auth service for the database resource's protocol
// so callers don't have to hard-code it. Uses GetDatabaseServers (heartbeats
// from db services) which mirrors what `tsh db ls` shows; the
// GetDatabases call returns the centralized Database registry which most
// users don't have RBAC to read directly.
func lookupProtocol(ctx context.Context, c *client.Client, name string) (string, error) {
	servers, err := c.GetDatabaseServers(ctx, defaults.Namespace)
	if err != nil {
		return "", fmt.Errorf("listing database servers: %w", err)
	}
	for _, s := range servers {
		db := s.GetDatabase()
		if db == nil {
			continue
		}
		if db.GetName() == name {
			return db.GetProtocol(), nil
		}
	}
	return "", fmt.Errorf("no Teleport database resource named %q is reachable with the current credentials", name)
}
