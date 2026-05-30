// Package dbcerts issues a short-lived database client certificate plus the
// cluster TLS CA bundle: the in-process equivalent of `tsh db login` +
// `tsh db config`. Used by both the db_certificate and db_tunnel resources.
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
	Database       string        // Teleport database service name
	Protocol       string        // e.g. "postgres"; empty => looked up
	DBUser         string        // embedded in the cert
	DBName         string        // embedded in the cert
	TTL            time.Duration // zero defaults to 1h
	RouteToCluster string        // empty => the proxy's own cluster
}

// Result is the material returned by Issue.
type Result struct {
	CertPEM  []byte
	KeyPEM   []byte // ECDSA P-256, PKCS#8
	CAPEM    []byte // cluster TLS CA bundle
	Protocol string // resolved protocol (useful when Request.Protocol was empty)
}

// Issue generates a keypair, has Teleport sign a database-scoped cert, and
// returns the cert material plus the cluster TLS CA bundle.
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

// lookupProtocol resolves the database's protocol via GetDatabaseServers
// (db_server heartbeats), which most roles can read, unlike GetDatabases.
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
