-- +goose Up

-- The end of instance-admin access (PRD §5.5, FR-TEN-02 AC4): an instance
-- admin who acts as the owner of a workspace through the InstanceAdminAccess
-- capability can make invitations that their own role there may not give,
-- and when that access ends, those whose link they hold are revoked (package
-- workspace). It ends for an account that grant-role makes no longer an
-- instance admin, and for every instance admin when a server starts with
-- the capability off after a server of the instance last started with it
-- on, which this column tells: the capability that a server last started
-- with, or null until a server records it, as on a database that a release
-- before this one used, which may have had it on.
ALTER TABLE instance ADD COLUMN instance_admin_access boolean;
GRANT UPDATE (instance_admin_access) ON instance TO storycreep_app;
