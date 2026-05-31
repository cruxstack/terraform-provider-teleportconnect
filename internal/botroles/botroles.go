// Package botroles resolves the roles a delegated-join (Machine ID) bot
// identity must impersonate to issue resource-scoped certificates.
//
// A bot authenticates as the user `bot-<name>` whose only role is the
// generated wrapper role `bot-<name>`. That wrapper grants db/ssh access not
// directly but via role impersonation of the bot's configured roles. So a
// plain GenerateUserCerts keyed on the bot user yields a certificate without
// the access roles, which the db/ssh agent then denies. tsh/tbot avoid this by
// requesting the impersonated roles (RoleRequests + UseRoleRequests); this
// package discovers that set from the wrapper role.
package botroles

import (
	"context"
	"strings"

	"github.com/gravitational/teleport/api/client"
	"github.com/gravitational/teleport/api/types"
)

// botRolePrefix is the name prefix of the generated wrapper role/user for a
// Machine ID bot.
const botRolePrefix = "bot-"

// ImpersonatedRoles returns the roles the current identity should request when
// issuing resource certificates. For a bot identity it returns the roles the
// wrapper role is allowed to impersonate; for a normal user it returns nil
// (the user's own roles already authorize the certificate).
func ImpersonatedRoles(ctx context.Context, c *client.Client) ([]string, error) {
	user, err := c.GetCurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	wrapper, ok := botWrapperRole(user.GetRoles())
	if !ok {
		return nil, nil
	}

	role, err := c.GetRole(ctx, wrapper)
	if err != nil {
		// Without read access to the wrapper role we cannot discover the
		// impersonated set; fall back to no impersonation rather than failing.
		return nil, nil //nolint:nilerr // intentional graceful fallback
	}
	return role.GetImpersonateConditions(types.Allow).Roles, nil
}

// botWrapperRole returns the wrapper role name and true if the role set belongs
// to a Machine ID bot (exactly one role, prefixed with "bot-").
func botWrapperRole(roles []string) (string, bool) {
	if len(roles) == 1 && strings.HasPrefix(roles[0], botRolePrefix) {
		return roles[0], true
	}
	return "", false
}
