BEGIN;

-- Modify "tiers" table
ALTER TABLE "public"."tiers" 
    DROP CONSTRAINT IF EXISTS "tiers_ram_mb_check",
    DROP CONSTRAINT IF EXISTS "tiers_vcpu_check",
    DROP COLUMN IF EXISTS "vcpu",
    DROP COLUMN IF EXISTS "ram_mb";

COMMIT; 