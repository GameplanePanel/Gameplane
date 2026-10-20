# OIDC provider setup

Gameplane supports OpenID Connect (OIDC) for single sign-on. Providers are managed dynamically through the dashboard and applied without restart.

## Prerequisites

Before adding a provider, set **Admin Settings → General → External URL** to your public dashboard hostname (e.g. `https://gameplane.example.com`). The API refuses to build providers without it — the redirect/callback URL is derived from this setting.

## Adding a provider

**Admin Settings → Authentication → Add provider**

Fill in:
- **Issuer URL**: the identity provider's OpenID configuration endpoint
- **Client ID**: the public identifier from your IdP
- **Client Secret**: stored in a Kubernetes Secret (automatic)
- **Display name** (optional): login button text; defaults to the provider name

The callback URL is automatically set to `{External URL}/auth/oidc/{provider-name}/callback`.

**Note**: The Helm-flag single OIDC provider supports group→role mapping via Helm values `api.oidc.groupsClaim` and `api.oidc.roleMappings.{admin,operator,viewer}` (or CLI flags `--oidc-groups-claim` and `--oidc-role-mapping-{admin,operator,viewer}`) (since v0.3.0). See "Role Mapping at Install Time (No Bootstrap-Admin Required)" below for setup details and worked examples.

## Provider guides

### Keycloak

1. Log in to your Keycloak realm as an admin.

2. **Create a realm client**:
   - Clients → Create client
   - Client type: OpenID Connect
   - Client ID: (choose one, e.g. `gameplane`)
   - Next → Standard flow enabled → Save

3. **Configure the client**:
   - In the client's Access settings:
     - Root URL: leave blank (Keycloak doesn't enforce this for OIDC)
     - Valid Redirect URIs: add `https://gameplane.example.com/auth/oidc/keycloak/callback`
     - Web Origins: add `https://gameplane.example.com`
     - Save

4. **Get credentials**:
   - Credentials tab → copy the Client Secret

5. **Find the issuer URL**:
   - Realm settings → Endpoints → OpenID Endpoint Configuration
   - Copy the URL without the `/.well-known/openid-configuration` suffix
   - Example: `https://keycloak.example.com/realms/master`

6. **Expose groups** (optional, for role mapping from v0.2.0-beta.6+): <!-- doc-versions: historical -->
   - Client scopes → Add builtin mapper → Group Membership
   - Token Mapper Type: Group Membership
   - Full group path: OFF (so claims use short group names, not `/root/group`)
   - Add to ID Token: ON

7. **In Gameplane**, set:
   - Issuer: `https://keycloak.example.com/realms/master` (from step 5)
   - Client ID: `gameplane`
   - Client Secret: (from step 4)

### Authentik

1. Log in to your Authentik instance as an admin.

2. **Create an OAuth2/OpenID provider**:
   - Applications → Providers → Create → OpenID Provider / Generic OAuth
   - Name: `gameplane` (or your choice)

3. **Configure the provider**:
   - Authorization flow: select a default OAuth2 authorization flow (usually the built-in one)
   - Save — Authentik generates a Client ID and Client Secret

4. **Get credentials**:
   - Copy the Client ID and Client Secret from the provider page

5. **Find the issuer URL**:
   - Settings → System → Tenant settings
   - Copy the Tenant Domain (e.g. `https://authentik.example.com`)
   - Issuer URL is: `https://authentik.example.com/application/o/<provider-slug>/`
   - The slug is shown on the provider's overview page

6. **Create an application** (binds the provider to your dashboard):
   - Applications → Applications → Create
   - Name: `gameplane`
   - Provider: select the provider you just created
   - Redirect URIs: add `https://gameplane.example.com/auth/oidc/authentik/callback`
   - Save

7. **Groups are included by default**:
   - Authentik's OIDC profile scope automatically returns `ak_groups` in the ID token
   - From v0.2.0-beta.6+, these can be mapped to Gameplane roles via Authentication settings <!-- doc-versions: historical -->

8. **In Gameplane**, set:
   - Issuer: `https://authentik.example.com/application/o/<slug>/`
   - Client ID: (from step 4)
   - Client Secret: (from step 4)

### Google

1. Go to [Google Cloud Console](https://console.cloud.google.com).

2. **Create an OAuth client**:
   - Select your project (create one if needed)
   - APIs & Services → Credentials → Create Credentials → OAuth client ID
   - Application type: Web application

3. **Configure the client**:
   - Authorized JavaScript origins: add `https://gameplane.example.com`
   - Authorized redirect URIs: add `https://gameplane.example.com/auth/oidc/google/callback`
   - Create

4. **Get credentials**:
   - Copy the Client ID and Client Secret

5. **In Gameplane**, set:
   - Issuer: `https://accounts.google.com`
   - Client ID: (from step 4)
   - Client Secret: (from step 4)

**Note**: Google does not issue a `groups` claim for free tier accounts. If you need group-based role mapping, you must set up [Workspace Directory API](https://developers.google.com/workspace/directory/v1) and use a custom identity provider configuration (not a standard OIDC setup). For most users, simply promoting new logins to the operator or admin role covers the use case.

### Okta

1. Log in to your Okta admin console.

2. **Create an OIDC application**:
   - Applications → Applications → Create App Integration
   - Sign-in method: OIDC - OpenID Connect
   - Application type: Web Application
   - Next

3. **Configure the application**:
   - App name: (choose one, e.g. `gameplane`)
   - Grant type: Authorization Code
   - Sign-in redirect URIs: `https://gameplane.example.com/auth/oidc/okta/callback`
   - Sign-out redirect URIs: `https://gameplane.example.com/auth/logout` (optional)
   - Save

4. **Get credentials**:
   - Copy the Client ID and Client Secret from the application settings

5. **Find the issuer URL**:
   - In the app's General tab, scroll to the OIDC section
   - Copy the Okta domain (e.g., `https://dev-123456.okta.com`)
   - Issuer URL is: `https://dev-123456.okta.com` (without `/oauth2/default`, Gameplane will discover the endpoint)

6. **Configure groups/roles claim**:
   - By default, Okta includes group names in the `groups` claim in the ID token
   - Consult Okta's current documentation to verify or customize the claim name and ensure it is emitted in the ID token
   - If you have custom role/group claims with a different name (e.g., `roles`, `departments`), note their exact name for Gameplane's `groupsClaim` setting

7. **In Gameplane** (via Helm values):
   - Issuer: `https://dev-123456.okta.com`
   - Client ID: (from step 4)
   - Client Secret: (from step 4)
   - Groups claim (Helm key `api.oidc.groupsClaim`): `groups` (Okta's default) or your custom claim name
   - Role mappings (Helm keys `api.oidc.roleMappings.*`): map Okta group names to dashboard roles

**Example Helm values** (Okta with groups):
```yaml
api:
  oidc:
    enabled: true
    issuer: "https://dev-123456.okta.com"
    clientID: "0oa123abc..."
    clientSecretRef:
      name: "okta-oidc-secret"
      key: "clientSecret"
    redirectURL: "https://gameplane.example.com/auth/oidc/okta/callback"
    displayName: "Okta"
    groupsClaim: "groups"
    roleMappings:
      admin:
        - "gameplane-admins"
        - "infrastructure-team"
      operator:
        - "gameplane-operators"
      viewer:
        - "gameplane-viewers"
    defaultRole: "viewer"
```

### Azure AD (Entra ID)

1. Log in to [Azure Portal](https://portal.azure.com) → Azure Active Directory.

2. **Register an application**:
   - App registrations → New registration
   - Name: (choose one, e.g. `gameplane`)
   - Supported account types: (choose based on your org; typically "Accounts in this organizational directory only")
   - Redirect URI:
     - Platform: Web
     - URI: `https://gameplane.example.com/auth/oidc/azuread/callback`
   - Register

3. **Get credentials**:
   - Copy the Application (client) ID and Directory (tenant) ID from the Overview page
   - Certificates & secrets → New client secret
   - Copy the client secret value (not the secret ID)

4. **Find the issuer URL**:
   - Endpoints → OpenID Connect metadata document
   - Extract the issuer from that URL (usually `https://login.microsoftonline.com/{tenant-id}/v2.0`)
   - Or construct it as: `https://login.microsoftonline.com/{tenant-id}/v2.0` (with your Directory ID from step 3)

5. **Configure groups claim** (CRITICAL for Azure AD):
   - **Azure AD does NOT emit group names by default** — you must explicitly configure it
   - Consult Azure AD's current documentation on how to enable group claims in your tenant's token configuration
   - When configuring, choose to emit either "Group ID" (stable identifiers) or "Group display name" (readable names), then map by whichever you chose in Gameplane's role mappings
   - Note the exact claim name your configuration emits (typically `roles`, but verify in your tenant)

6. **Get group IDs or names**:
   - After enabling groups, find the Azure AD group you want to map
   - Depending on what you selected in step 5:
     - If "Group ID": copy the Object ID
     - If "Group display name": copy the Display Name (e.g., `gameplane-admins`)
   - Note these values for Gameplane role mappings

7. **In Gameplane** (via Helm values):
   - Issuer: `https://login.microsoftonline.com/{tenant-id}/v2.0` (with your Directory ID)
   - Client ID: (from step 3)
   - Client Secret: (from step 3)
   - Groups claim (Helm key `api.oidc.groupsClaim`): Azure AD's default is `roles`, but verify the exact claim name in your tenant's token configuration (it is configurable)
   - Role mappings (Helm keys `api.oidc.roleMappings.*`): map Azure AD group IDs or display names (whichever you chose in step 5) to dashboard roles

**Example Helm values** (Azure AD with group display names):
```yaml
api:
  oidc:
    enabled: true
    issuer: "https://login.microsoftonline.com/12345678-1234-1234-1234-123456789012/v2.0"
    clientID: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
    clientSecretRef:
      name: "azuread-oidc-secret"
      key: "clientSecret"
    redirectURL: "https://gameplane.example.com/auth/oidc/azuread/callback"
    displayName: "Azure AD"
    groupsClaim: "roles"
    roleMappings:
      admin:
        - "gameplane-admins"
        - "Platform Engineering"
      operator:
        - "gameplane-operators"
      viewer:
        - "All Users"
    defaultRole: "viewer"
```

## Role Mapping at Install Time (No Bootstrap-Admin Required)

With Helm values configured, you can deploy Gameplane with OIDC and role mappings enabled *without* running the `bootstrap-admin` command. The first user to log in will receive their role based on their IdP group membership, with no manual admin step required.

### Worked Example: OIDC-Only Install with Keycloak

Suppose you have a Keycloak realm with groups:
- `gameplane-admins` (infrastructure team)
- `gameplane-operators` (on-call support)
- `gameplane-viewers` (read-only access)

**1. Configure Keycloak groups claim** (already covered in the Keycloak section above):
   - Add the Group Membership mapper to your client scope
   - Ensure "Add to ID Token" is ON

**2. Deploy Gameplane with Helm values**:

```bash
helm install gameplane ./charts/gameplane \
  --namespace gameplane \
  --set api.oidc.enabled=true \
  --set api.oidc.issuer="https://keycloak.example.com/realms/master" \
  --set api.oidc.clientID="gameplane" \
  --set api.oidc.clientSecretRef.name="keycloak-oidc-secret" \
  --set api.oidc.clientSecretRef.key="clientSecret" \
  --set api.oidc.redirectURL="https://gameplane.example.com/auth/oidc/keycloak/callback" \
  --set api.oidc.displayName="Keycloak" \
  --set api.oidc.groupsClaim="groups" \
  --set 'api.oidc.roleMappings.admin={gameplane-admins}' \
  --set 'api.oidc.roleMappings.operator={gameplane-operators}' \
  --set 'api.oidc.roleMappings.viewer={gameplane-viewers}' \
  --set api.oidc.defaultRole="viewer"
```

Or in `values.yaml`:
```yaml
api:
  oidc:
    enabled: true
    issuer: "https://keycloak.example.com/realms/master"
    clientID: "gameplane"
    clientSecretRef:
      name: "keycloak-oidc-secret"
      key: "clientSecret"
    redirectURL: "https://gameplane.example.com/auth/oidc/keycloak/callback"
    displayName: "Keycloak"
    groupsClaim: "groups"
    roleMappings:
      admin:
        - "gameplane-admins"
      operator:
        - "gameplane-operators"
      viewer:
        - "gameplane-viewers"
    defaultRole: "viewer"
```

**3. First user login**:
   - Navigate to the Gameplane dashboard login page
   - Click the Keycloak login button
   - Log in with a Keycloak account that belongs to the `gameplane-admins` group
   - Upon successful login, the user is created in Gameplane with the **admin role** — no additional step needed
   - The user can immediately access all admin features

**4. No bootstrap-admin needed**:
   - Because role mappings are configured, every user receives their role on first login based on their group membership
   - The `bootstrap-admin` command is optional — use it only if you need to create a local password account as a break-glass mechanism

**5. Subsequent login role re-evaluation**:
   - If a user's group membership changes in Keycloak (e.g., promoted to `gameplane-admins`), their Gameplane role is re-evaluated and updated on their next login
   - The dashboard shows their new role immediately after login

### User Behavior Without a Group Match

If a user logs in and their IdP groups do not match any configured mapping (or the groups claim is absent from their token):
- The user receives the role specified by `api.oidc.defaultRole` (default: `viewer`)
- If `defaultRole` is set to `"deny"`, the login is rejected and the user is not created
- Once logged in, an admin can manually promote the user via the Users page (sidebar: Users & RBAC) if needed

**Note**: `groupsClaim` and `defaultRole` are **Helm-only settings in v1** — they are set at install time via Helm values and cannot be changed from the dashboard. Only `roleMappings` can be overridden by admins through the dashboard (see below).

## Managing Role Mappings from the Dashboard

Once a Gameplane instance is running with Helm-seeded role mappings, admins can override any role's group list directly from the dashboard — **no Helm upgrade or API restart required**. Changes take effect on the next OIDC login for that user.

### Override a Role's Group Mapping

1. Navigate to **Admin Settings → Authentication**

2. Locate the Helm-configured provider section (displayed as read-only)

3. For each role (`admin`, `operator`, `viewer`), you'll see:
   - The **Helm-seeded value** (groups configured at install time)
   - An option to **override** this list

4. Click the override button for a role and enter a comma-separated list of group names:
   - Example: for the admin role, enter `gameplane-admins, infrastructure-team, devops-leads`
   - An empty list (`[]`) is valid and means "nobody gets this role from a group match"

5. Click **Save**

The override takes effect immediately on the very next OIDC login for that role — no API restart, no `helm upgrade`, no redeployment. Users who already hold that role keep it on their current session; users on new logins are assigned roles based on the updated mapping.

### Reset a Role Back to Its Helm Default

If you've overridden a role's group list and later want to restore the original Helm-seeded value:

1. Navigate to **Admin Settings → Authentication**

2. For the role you want to reset, click the **reset** button (shown only if the role is currently overridden)

3. Confirm the action

The role is immediately restored to the Helm-seeded group list. If users are currently logged in with that role, they keep it on their current session; on next login, the role assignment uses the reset mapping.

### Precedence: Most-Privileged Match Wins

Gameplane matches a user's IdP groups against each role's group list in a priority order: **admin > operator > viewer**. The user receives the most privileged matching role.

**Scenario**: Suppose you have:
- Helm-seeded admin groups: `gameplane-admins`
- Helm-seeded operator groups: `gameplane-operators`
- DB override for viewer groups: `dev-team`

A user in both `gameplane-admins` (admin, Helm-seeded) and `dev-team` (viewer, DB-overridden) receives the **admin role** — the override for viewer does not prevent the match against the Helm-seeded admin list. This is by design: admin overrides don't restrict higher-privilege roles.

## Troubleshooting

| Issue | Check |
|-------|-------|
| "issuer discovery failed" (login page won't load) | **Wrong issuer URL or TLS failure.** Verify the exact issuer URL in your IdP's OIDC endpoints, including trailing slash if present. Test locally: `curl https://issuer/.well-known/openid-configuration`. If the IdP uses a private CA, the API container must trust it — there is no chart value for this today, so either use a publicly-trusted certificate on the IdP or mount your CA into the API pod's system trust store. |
| "redirect_uri mismatch" at login | **External URL changed after registering the app.** If you set a new `External URL` in Admin Settings, update the redirect URI in your IdP's client config to match `{new-url}/auth/oidc/{provider-name}/callback`. |
| "state mismatch" (blank login error) | **Cookies blocked or clock skew.** Ensure your browser accepts session cookies (`gameplane_oidc_state`, `gameplane_oidc_nonce`) and that server and IdP clocks are synchronized (within 5 minutes). Check browser DevTools → Network/Cookies for blocked third-party cookies if in an iframe. |
| Login succeeds but user is stuck at viewer role | **New OIDC users start as viewer.** An admin must promote them on the Users page (sidebar: Users & RBAC). From v0.2.0-beta.6+, group claims can map to roles automatically; check Authentication settings for group-based role mapping. <!-- doc-versions: historical --> |
| Login succeeds but role did not update when groups changed at IdP | **Last-admin demotion guard.** When group→role mapping is enabled, users' roles are re-synced from their IdP groups on each login. To prevent lockout, the system blocks any role change that would remove the *last* user capable of managing other users (i.e., the last admin or user-manager). If your group assignment change would demote the sole user-manager, the demotion is silently skipped: the login succeeds, but the user's stored role remains unchanged. To confirm the demotion guard was triggered, check the API pod logs for a warn-level entry containing `oidc role resync skipped`. Verify you have at least one other admin or user-manager in a different group, then have the affected user log in again. |
| User created in one IdP logs in as a different account after linking IdP accounts | **Account linking is by OIDC identity, not email.** OIDC identities are permanently linked to Gameplane accounts by (issuer, subject) — the combination of your identity provider and the user's subject claim (a unique per-provider identifier, not email). If a user's email address or display name changes at the IdP, they will still log in as the same Gameplane account. However, if you add a different IdP or reconfigure an existing one to use a different subject attribute, that user will be treated as a new person on first login with the new issuer/subject pair. To prevent duplicate accounts, ensure your IdP subject claim is stable and won't change if you reconfigure the provider. |
| Provider button missing from login page | **Provider disabled or secret missing.** Verify in Admin Settings → Authentication that the provider is enabled and has a client secret stored. If the secret was deleted from the control-plane namespace (label `gameplane.local/auth-provider=true`), recreate it with the exact name shown in the provider's config. |
