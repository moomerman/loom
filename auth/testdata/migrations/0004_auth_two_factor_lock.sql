-- loom:from github.com/moomerman/loom/auth 0004_two_factor_lock.sql
-- Installed by `loom db install github.com/moomerman/loom/auth`; applied by `loom migrate` like the app's own.

-- Wrong two-factor codes per account, across sign-ins. A challenge takes
-- five wrong codes before the person starts again from the password; this
-- counts every wrong code, on any challenge or the account page, and ten
-- in a row lock the account's codes for fifteen minutes. A right code
-- resets the count.
ALTER TABLE auth_totp
	ADD COLUMN failed int NOT NULL DEFAULT 0,  -- wrong codes since the last right one
	ADD COLUMN locked_until timestamptz;       -- no code is accepted before this
