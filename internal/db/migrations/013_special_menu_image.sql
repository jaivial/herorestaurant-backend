-- Add special menu image column to menusDeGrupos
ALTER TABLE `menusDeGrupos` ADD COLUMN `special_menu_image_url` VARCHAR(512) NULL AFTER `editor_version`;
