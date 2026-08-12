-- Remember which proxy service the admin/user picked on the 代理 tab when
-- creating a rule (display + client copy/QR). NULL/0 = plain node pick.
-- Does not change the data plane: grants and hops still key on node_id.
ALTER TABLE rules ADD COLUMN proxy_service_id INTEGER NOT NULL DEFAULT 0;
