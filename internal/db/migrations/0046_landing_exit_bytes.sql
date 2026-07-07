ALTER TABLE rules ADD COLUMN exit_bytes INTEGER NOT NULL DEFAULT 0;

-- Backfill rules.exit_bytes from the final hop's total_bytes (raw up+down).
UPDATE rules
SET exit_bytes = COALESCE((
    SELECT SUM(total_bytes)
    FROM rule_hops
    WHERE rule_hops.rule_id = rules.id
      AND rule_hops.position = (
          SELECT MAX(position)
          FROM rule_hops h2
          WHERE h2.rule_id = rules.id
      )
), 0);

-- Re-align users.traffic_used_bytes to the sum of current rules' landing traffic.
-- This makes the user dashboard immediately consistent with the rule list after
-- the model switch. The cumulative total is never decreased.
UPDATE users
SET traffic_used_bytes = COALESCE((
    SELECT SUM(exit_bytes)
    FROM rules
    WHERE rules.owner_id = users.id
), 0),
    total_traffic_used_bytes = MAX(
        total_traffic_used_bytes,
        COALESCE((
            SELECT SUM(exit_bytes)
            FROM rules
            WHERE rules.owner_id = users.id
        ), 0)
    );
