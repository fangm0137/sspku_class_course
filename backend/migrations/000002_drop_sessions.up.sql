-- Authentication is stateless now: login signs a JWT and every protected request
-- carries it in the Authorization header. The server keeps no replayable session
-- record, so the session table created by 000001 is dropped here instead of being
-- left behind as unused state. 000001 stays untouched so upgrades apply in place.
DROP TABLE IF EXISTS sessions;
