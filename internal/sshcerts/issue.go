// Package sshcerts wraps the Teleport API call needed to issue a short-lived
// SSH user certificate plus the SSH host CA bundle required to verify
// Teleport-managed SSH servers.
//
// This is the in-process equivalent of the SSH-cert side of `tsh login`.
package sshcerts

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/gravitational/teleport/api/client"
	"github.com/gravitational/teleport/api/client/proto"
	"github.com/gravitational/teleport/api/constants"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"golang.org/x/crypto/ssh"
)

// Request describes a single SSH certificate issuance.
type Request struct {
	// NodeName is the Teleport node hostname/UUID the cert should target.
	// Required - the resulting cert is scoped to this node.
	NodeName string
	// SSHLogin is the OS user the cert authorizes the bearer to connect as
	// (e.g. "root", "ec2-user").
	SSHLogin string
	// TTL is the certificate validity. Zero defaults to 1h.
	TTL time.Duration
	// RouteToCluster overrides the route_to_cluster field. Empty means
	// "the cluster the proxy belongs to".
	RouteToCluster string
}

// Result is the material returned by Issue.
type Result struct {
	// SSHCert is the parsed Teleport-signed SSH certificate.
	SSHCert *ssh.Certificate
	// PrivateKey is the ECDSA P-256 private key matching the cert,
	// exposed as a crypto.Signer. Caller owns it.
	PrivateKey crypto.Signer
	// SSHCAs are the PEM-encoded SSH CA public keys (in OpenSSH
	// authorized_keys format) for verifying SSH host keys of cluster nodes.
	SSHCAs [][]byte
}

// Issue generates a fresh RSA keypair, asks the Teleport auth service to
// sign an SSH-scoped user certificate for it, and returns the cert, private
// key, and SSH CAs.
func Issue(ctx context.Context, c *client.Client, req Request) (*Result, error) {
	if c == nil {
		return nil, errors.New("teleport client is nil")
	}
	if req.NodeName == "" {
		return nil, errors.New("Request.NodeName is required")
	}
	if req.SSHLogin == "" {
		return nil, errors.New("Request.SSHLogin is required")
	}

	ttl := req.TTL
	if ttl <= 0 {
		ttl = time.Hour
	}

	tpUser, err := c.GetCurrentUser(ctx)
	if err != nil {
		return nil, fmt.Errorf("fetching current user: %w", err)
	}

	tflog.Debug(ctx, "generating ecdsa keypair for ssh certificate", map[string]any{
		"node_name": req.NodeName,
		"ssh_login": req.SSHLogin,
		"user":      tpUser.GetName(),
	})

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating private key: %w", err)
	}

	sshSigner, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("building ssh signer: %w", err)
	}
	sshPub := ssh.MarshalAuthorizedKey(sshSigner.PublicKey())

	certs, err := c.GenerateUserCerts(ctx, proto.UserCertsRequest{
		Username:       tpUser.GetName(),
		SSHPublicKey:   sshPub,
		Expires:        time.Now().Add(ttl),
		Format:         constants.CertificateFormatStandard,
		Usage:          proto.UserCertsRequest_SSH,
		NodeName:       req.NodeName,
		SSHLogin:       req.SSHLogin,
		RouteToCluster: req.RouteToCluster,
	})
	if err != nil {
		return nil, fmt.Errorf("issuing ssh certificate: %w", err)
	}

	pub, _, _, _, err := ssh.ParseAuthorizedKey(certs.SSH)
	if err != nil {
		return nil, fmt.Errorf("parsing ssh certificate: %w", err)
	}
	sshCert, ok := pub.(*ssh.Certificate)
	if !ok {
		return nil, errors.New("auth server returned non-certificate ssh key")
	}

	return &Result{
		SSHCert:    sshCert,
		PrivateKey: priv,
		SSHCAs:     certs.SSHCACerts,
	}, nil
}
