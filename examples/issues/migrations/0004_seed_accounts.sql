-- Sign-in accounts for the example's people, with the ids of their rows
-- in users, so the id auth signs in is the id the tracker uses. They
-- share the demo password "loom-issues" (an auth.HashPassword hash).
-- Their emails count as verified: they're the workspace's own people.
INSERT INTO auth_users (id, email, password_hash, email_verified_at)
SELECT id, email, 'pbkdf2-sha256$600000$ufgNBL/AC0/FdzOiVFXEXg$1HHXauiKXFJ+WwnWkGEjzL52zGjzEUnTmt6HNBu0bG4', now()
FROM users;
