# SSO user profiles

OIDC sign-in reads `preferred_username` and falls back to the IdP's userinfo endpoint when the
ID token leaves out email, name or username. Each login refreshes an existing user's email and
display name. SSO users whose username is still the raw subject ID are renamed to their
`preferred_username` (or email) on their next login. [PR #928](https://github.com/GameplanePanel/Gameplane/pull/928)
