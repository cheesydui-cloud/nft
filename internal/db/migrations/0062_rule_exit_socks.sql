-- SOCKS5 exit for chain rules: exit_host/exit_port remain the CONNECT target;
-- exit_uri holds socks5://user:pass@proxy:port; exit_type selects the mode.
-- exit_uri already exists (0010); re-enable it for product SOCKS5 (not landing URI).
ALTER TABLE rules ADD COLUMN exit_type TEXT NOT NULL DEFAULT 'direct';
