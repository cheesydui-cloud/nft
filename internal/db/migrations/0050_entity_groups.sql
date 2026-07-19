-- Free-form admin groups for the node repository and user list.
ALTER TABLE node_repo ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN group_name TEXT NOT NULL DEFAULT '';
