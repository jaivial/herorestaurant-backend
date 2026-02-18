-- Add price column to group_menu_section_dishes_v2 for a la carte menus
ALTER TABLE `group_menu_section_dishes_v2` ADD COLUMN `price` DECIMAL(10,2) NULL AFTER `supplement_price`;
