# SSO group role rules

An SSO provider maps IdP groups to roles with an ordered list of rules, and
a rule can name any role, including custom roles such as `login-only`. The
first matching rule wins. Helm values take the same list as
`api.oidc.roleRules`. The old `roleMappings` keys (admin, operator, viewer)
keep working and give the same result as before, but cannot be combined with
`roleRules` on the same provider.

A role changed by hand on the Users page is now kept at sign-in. Gameplane
records whether each user's role came from SSO or was set by hand. Hand-set
roles are not overwritten at sign-in, and an admin can return a user to SSO
control. Existing users keep their current roles at upgrade: users linked to
SSO start as SSO-managed and all other users start as set by hand. Each SSO
sign-in writes an audit event that gives the matched rule and whether the
role was applied.
