// Package sshcerts issues a short-lived SSH user certificate plus the SSH host
// CA bundle for verifying Teleport servers: the in-process equivalent of the
// SSH-cert side of `tsh login`.
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

	"github.com/cruxstack/terraform-provider-teleportconnect/internal/botroles"
)

// Request describes a single SSH certificate issuance.
type Request struct {
	NodeName       string        // target node hostname/UUID; the cert is scoped to it
	SSHLogin       string        // OS user to connect as, e.g. "root"
	TTL            time.Duration // zero defaults to 1h
	RouteToCluster string        // empty => the proxy's own cluster
}

// Result is the material returned by Issue.
type Result struct {
	SSHCert    *ssh.Certificate
	PrivateKey crypto.Signer // ECDSA P-256
	SSHCAs     [][]byte      // SSH host CAs (authorized_keys format) for host-key verification
}

// Issue generates a keypair, has Teleport sign an SSH-scoped cert, and returns
// the cert, private key, and SSH CAs.
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

	// A bot identity must impersonate its configured roles for the issued cert
	// to carry ssh access; a normal user gets nil (its own roles apply).
	roleRequests, err := botroles.ImpersonatedRoles(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("resolving impersonated roles: %w", err)
	}

	tflog.Debug(ctx, "generating ecdsa keypair for ssh certificate", map[string]any{
		"node_name":     req.NodeName,
		"ssh_login":     req.SSHLogin,
		"user":          tpUser.GetName(),
		"role_requests": roleRequests,
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
		Username:        tpUser.GetName(),
		SSHPublicKey:    sshPub,
		Expires:         time.Now().Add(ttl),
		Format:          constants.CertificateFormatStandard,
		Usage:           proto.UserCertsRequest_SSH,
		NodeName:        req.NodeName,
		SSHLogin:        req.SSHLogin,
		RouteToCluster:  req.RouteToCluster,
		RoleRequests:    roleRequests,
		UseRoleRequests: len(roleRequests) > 0,
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
