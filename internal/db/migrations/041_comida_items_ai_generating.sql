-- Add ai_generating column to track pending AI image enhancement jobs
ALTER TABLE comida_items ADD COLUMN ai_generating TINYINT(1) NOT NULL DEFAULT 0;
