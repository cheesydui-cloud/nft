-- Manual list bucket for the node management page (单点/组合/落地).
-- Empty = default (single vs composite still driven by node_type).
-- 'landing' = shown under the 落地 tab and hidden from 单点/组合.
ALTER TABLE nodes ADD COLUMN list_group TEXT NOT NULL DEFAULT '';
