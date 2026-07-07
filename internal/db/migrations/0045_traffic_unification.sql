ALTER TABLE rule_hops ADD COLUMN billed_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN total_traffic_used_bytes INTEGER NOT NULL DEFAULT 0;

-- Backfill rule_hops.billed_bytes for entry hops from the current total_bytes.
-- Old total_bytes at position=0 already included the user's billing_rate, so we
-- divide by the user's billing_rate to recover the rate-neutral billed base.
-- Rows where the owner or billing_rate cannot be resolved keep total_bytes as-is.
UPDATE rule_hops
SET billed_bytes = (
    SELECT CASE
        WHEN u.billing_rate > 0 THEN CAST(rule_hops.total_bytes / u.billing_rate AS INTEGER)
        ELSE rule_hops.total_bytes
    END
    FROM rules r
    LEFT JOIN users u ON u.id = r.owner_id
    WHERE r.id = rule_hops.rule_id
)
WHERE position = 0;

-- Backfill users.total_traffic_used_bytes and convert users.traffic_used_bytes
-- to the new rate-neutral base.
UPDATE users
SET total_traffic_used_bytes = CASE WHEN billing_rate > 0 THEN CAST(traffic_used_bytes / billing_rate AS INTEGER) ELSE traffic_used_bytes END,
    traffic_used_bytes = CASE WHEN billing_rate > 0 THEN CAST(traffic_used_bytes / billing_rate AS INTEGER) ELSE traffic_used_bytes END;
