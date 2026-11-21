-- Spec 034: where each user's role came from. users.role_source records it:
--   sso          sign-in applies the SSO rule result to the role
--   sso_pending  returned to SSO control and not signed in since; the next
--                sign-in applies the rules
--   manual       set by hand; sign-in never changes the role
-- SSO-linked users start as sso, everyone else as manual, so existing
-- accounts keep their current roles until their next sign-in. Portable SQL:
-- runs unchanged on SQLite and PostgreSQL.
ALTER TABLE users ADD COLUMN role_source TEXT NOT NULL DEFAULT 'manual';
UPDATE users SET role_source = 'sso' WHERE id IN (SELECT user_id FROM oidc_links);
