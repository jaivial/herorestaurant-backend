-- Multitenant tracking: attach all restaurant-owned rows to a restaurant.
-- For Villa Carmen (existing data), everything defaults to restaurant_id=1.

ALTER TABLE `DIA`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_DIA_restaurant_tipo_active` (`restaurant_id`, `TIPO`, `active`);

ALTER TABLE `FINDE`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_FINDE_restaurant_tipo_active` (`restaurant_id`, `TIPO`, `active`);

ALTER TABLE `POSTRES`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_POSTRES_restaurant_active` (`restaurant_id`, `active`);

ALTER TABLE `VINOS`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_VINOS_restaurant_tipo_active` (`restaurant_id`, `tipo`, `active`);

ALTER TABLE `bookings`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_bookings_restaurant_date_status` (`restaurant_id`, `reservation_date`, `status`);

ALTER TABLE `cancelled_bookings`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_cancelled_restaurant_reservation_date` (`restaurant_id`, `reservation_date`);

ALTER TABLE `menu_visibility`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  DROP INDEX `menu_key`,
  ADD UNIQUE KEY `uniq_menu_visibility_restaurant_key` (`restaurant_id`, `menu_key`);

ALTER TABLE `menusDeGrupos`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_menusDeGrupos_restaurant_active` (`restaurant_id`, `active`);

ALTER TABLE `daily_limits`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  DROP INDEX `date`,
  ADD UNIQUE KEY `uniq_daily_limits_restaurant_date` (`restaurant_id`, `date`);

ALTER TABLE `hour_configuration`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  DROP INDEX `date`,
  ADD UNIQUE KEY `uniq_hour_configuration_restaurant_date` (`restaurant_id`, `date`),
  ADD KEY `idx_hour_configuration_restaurant_date` (`restaurant_id`, `date`);

ALTER TABLE `hours_percentage`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  DROP INDEX `reservationDate`,
  ADD UNIQUE KEY `uniq_hours_percentage_restaurant_date` (`restaurant_id`, `reservationDate`),
  ADD KEY `idx_hours_percentage_restaurant_date` (`restaurant_id`, `reservationDate`);

ALTER TABLE `mesas_de_dos`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  DROP INDEX `reservationDate`,
  ADD UNIQUE KEY `uniq_mesas_de_dos_restaurant_date` (`restaurant_id`, `reservationDate`);

ALTER TABLE `openinghours`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  DROP INDEX `unique_date`,
  ADD UNIQUE KEY `uniq_openinghours_restaurant_date` (`restaurant_id`, `dateselected`);

ALTER TABLE `reservation_manager`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_reservation_manager_restaurant_date` (`restaurant_id`, `reservationDate`);

ALTER TABLE `restaurant_days`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  DROP INDEX `date`,
  ADD UNIQUE KEY `uniq_restaurant_days_restaurant_date` (`restaurant_id`, `date`);

ALTER TABLE `salon_condesa`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  DROP INDEX `date`,
  ADD UNIQUE KEY `uniq_salon_condesa_restaurant_date` (`restaurant_id`, `date`);

ALTER TABLE `bot_conversation_messages`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_bot_conv_restaurant_phone_timestamp` (`restaurant_id`, `phone_number`, `timestamp`);

ALTER TABLE `conversation_messages`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_conv_messages_restaurant_sender_created` (`restaurant_id`, `sender_number`, `created_at`);

ALTER TABLE `conversation_sessions`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_conv_sessions_restaurant_sender` (`restaurant_id`, `sender_number`, `status`);

ALTER TABLE `conversation_states`
  ADD COLUMN `restaurant_id` INT NOT NULL DEFAULT 1,
  ADD KEY `idx_conv_states_restaurant_sender_state` (`restaurant_id`, `sender_number`, `conversation_state`);

