-- GENERATED FILE — do not edit by hand.
-- Source of truth: internal/domain/vocabulary.go (domain.Permissions).
-- Regenerate: go generate ./internal/domain/...
-- TestPermissionSeedMatchesGoSet fails CI if this file and the Go slice diverge.

INSERT INTO app.permission (permission, description, category) VALUES
    ('characters.view', 'View character sheets, skills and clones', 'characters'),
    ('characters.manage_tokens', 'Add or refresh a character''s ESI token', 'characters'),
    ('characters.revoke_tokens', 'Revoke a character''s ESI token', 'characters'),
    ('corporations.view', 'View corporation structures, wallets and members', 'corporations'),
    ('corporations.manage', 'Manage a tracked corporation''s sync configuration', 'corporations'),
    ('squads.view', 'View squad rosters and applications', 'squads'),
    ('squads.create', 'Create a new squad', 'squads'),
    ('squads.manage', 'Edit squad settings, roles and membership', 'squads'),
    ('squads.moderate', 'Approve or reject squad applications', 'squads'),
    ('squads.apply', 'Apply to join a squad', 'squads'),
    ('admin.settings.manage', 'Change installation-wide runtime settings', 'admin'),
    ('admin.users.manage', 'Manage user accounts and role assignment', 'admin'),
    ('admin.roles.manage', 'Create or edit RBAC roles and grants', 'admin'),
    ('admin.security_log.view', 'View the append-only security log', 'admin'),
    ('admin.esi_routes.manage', 'Edit route catalogue overrides (blocked_by_pin, TTL)', 'admin'),
    ('admin.esi_pin.advance', 'Advance the ESI compatibility date pin', 'admin'),
    ('provisioning.platforms.manage', 'Configure Discord/TeamSpeak/Mumble platform connections', 'provisioning'),
    ('provisioning.entitlements.manage', 'Edit entitlement rules that grant platform groups', 'provisioning'),
    ('provisioning.audit.view', 'View the provisioning audit trail and exposure board', 'provisioning'),
    ('alerting.channels.manage', 'Configure alert delivery channels', 'alerting'),
    ('alerting.routing.manage', 'Edit alert routing rules', 'alerting'),
    ('alerting.unknown_types.acknowledge', 'Acknowledge unrecognised notification types', 'alerting'),
    ('api_tokens.manage', 'Create or revoke third-party API tokens', 'api'),
    ('api_tokens.view_access_log', 'View third-party API token access logs', 'api'),
    ('webhooks.manage', 'Create or revoke outbound webhook endpoints', 'api')
ON CONFLICT (permission) DO UPDATE
   SET description = EXCLUDED.description,
       category    = EXCLUDED.category;
