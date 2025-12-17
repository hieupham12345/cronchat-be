-- =========================================
-- CronChat schema (clean)
-- =========================================
SET NAMES utf8mb4;
SET FOREIGN_KEY_CHECKS = 0;

-- Drop order: child -> parent (avoid FK errors)
DROP TABLE IF EXISTS `messages`;
DROP TABLE IF EXISTS `room_members`;
DROP TABLE IF EXISTS `rooms`;
DROP TABLE IF EXISTS `users`;

-- =========================================
-- USERS
-- =========================================
CREATE TABLE `users` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,

  `username` varchar(191) COLLATE utf8mb4_unicode_ci NOT NULL,
  `password` varchar(255) COLLATE utf8mb4_unicode_ci NOT NULL,
  `role` enum('user','admin','superadmin') COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'user',

  `full_name` varchar(255) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `email` varchar(255) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `phone` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `avatar_url` text COLLATE utf8mb4_unicode_ci,

  `is_active` tinyint(1) NOT NULL DEFAULT 1,
  `last_login` datetime DEFAULT NULL,
  `login_ip` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `created_ip` varchar(50) COLLATE utf8mb4_unicode_ci DEFAULT NULL,

  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_users_username` (`username`),
  KEY `idx_users_full_name` (`full_name`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- =========================================
-- ROOMS
-- =========================================
CREATE TABLE `rooms` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,

  `name` varchar(255) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `type` enum('direct','group') COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'direct',
  `created_by` int unsigned NOT NULL,

  `is_active` tinyint(1) NOT NULL DEFAULT 1,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,

  PRIMARY KEY (`id`),
  KEY `idx_rooms_type` (`type`),
  KEY `idx_rooms_created_by` (`created_by`),
  CONSTRAINT `fk_rooms_created_by`
    FOREIGN KEY (`created_by`) REFERENCES `users` (`id`)
    ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- =========================================
-- ROOM_MEMBERS
-- =========================================
CREATE TABLE `room_members` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,

  `room_id` int unsigned NOT NULL,
  `user_id` int unsigned NOT NULL,
  `member_role` enum('member','admin','owner') COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'member',

  `joined_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `last_seen_at` datetime DEFAULT NULL,

  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_room_members_room_user` (`room_id`,`user_id`),
  KEY `idx_room_members_user_id` (`user_id`),

  CONSTRAINT `fk_room_members_room`
    FOREIGN KEY (`room_id`) REFERENCES `rooms` (`id`)
    ON DELETE CASCADE,
  CONSTRAINT `fk_room_members_user`
    FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
    ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- =========================================
-- MESSAGES
-- =========================================
CREATE TABLE `messages` (
  `id` int unsigned NOT NULL AUTO_INCREMENT,

  `room_id` int unsigned NOT NULL,
  `sender_id` int unsigned NOT NULL,

  `content` text COLLATE utf8mb4_unicode_ci,
  `message_type` enum('text','image','file','system') COLLATE utf8mb4_unicode_ci NOT NULL DEFAULT 'text',
  `is_temp` tinyint(1) NOT NULL DEFAULT 0,

  `media_url` text COLLATE utf8mb4_unicode_ci,
  `media_mime` varchar(100) COLLATE utf8mb4_unicode_ci DEFAULT NULL,
  `media_size` bigint DEFAULT NULL,

  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime DEFAULT NULL ON UPDATE CURRENT_TIMESTAMP,

  PRIMARY KEY (`id`),
  KEY `idx_messages_room_id_created_at` (`room_id`,`created_at`),
  KEY `idx_messages_sender_id_created_at` (`sender_id`,`created_at`),

  CONSTRAINT `fk_messages_room`
    FOREIGN KEY (`room_id`) REFERENCES `rooms` (`id`)
    ON DELETE CASCADE,
  CONSTRAINT `fk_messages_sender`
    FOREIGN KEY (`sender_id`) REFERENCES `users` (`id`)
    ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE utf8mb4_unicode_ci;

SET FOREIGN_KEY_CHECKS = 1;


CREATE TABLE `message_reactions` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `message_id` INT UNSIGNED NOT NULL,
  `user_id` INT UNSIGNED NOT NULL,
  `reaction` VARCHAR(32) NOT NULL, -- 'like', '❤️', '😂', ...
  `created_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_reaction_message_user_reaction` (`message_id`,`user_id`,`reaction`),
  KEY `idx_reaction_message_id` (`message_id`),
  KEY `idx_reaction_user_id` (`user_id`),

  CONSTRAINT `fk_reactions_message`
    FOREIGN KEY (`message_id`) REFERENCES `messages` (`id`)
    ON DELETE CASCADE,
  CONSTRAINT `fk_reactions_user`
    FOREIGN KEY (`user_id`) REFERENCES `users` (`id`)
    ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

ALTER TABLE `messages`
  ADD COLUMN `reply_to_message_id` INT UNSIGNED NULL AFTER `sender_id`,
  ADD KEY `idx_messages_reply_to` (`reply_to_message_id`),
  ADD CONSTRAINT `fk_messages_reply_to`
    FOREIGN KEY (`reply_to_message_id`) REFERENCES `messages` (`id`)
    ON DELETE SET NULL;

ALTER TABLE `messages`
  ADD COLUMN `reply_preview` VARCHAR(300) NULL AFTER `reply_to_message_id`,
  ADD COLUMN `reply_sender_name` VARCHAR(255) NULL AFTER `reply_preview`,
  ADD COLUMN `reply_message_type` ENUM('text','image','file','system') NULL AFTER `reply_sender_name`;

CREATE TABLE `message_receipts` (
  `id` BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  `room_id` INT UNSIGNED NOT NULL,
  `message_id` INT UNSIGNED NOT NULL,
  `user_id` INT UNSIGNED NOT NULL,
  `status` ENUM('delivered','seen') NOT NULL DEFAULT 'seen',
  `seen_at` DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,

  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_receipt_message_user` (`message_id`,`user_id`),
  KEY `idx_receipt_room_user` (`room_id`,`user_id`,`message_id`),
  KEY `idx_receipt_message` (`message_id`),

  CONSTRAINT `fk_receipt_room`
    FOREIGN KEY (`room_id`) REFERENCES `rooms` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_receipt_message`
    FOREIGN KEY (`message_id`) REFERENCES `messages` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_receipt_user`
    FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;



CREATE DEFINER=`root`@`localhost` PROCEDURE `sp_send_message_with_day_sep`(
  IN p_room_id INT UNSIGNED,
  IN p_sender_id INT UNSIGNED,
  IN p_content TEXT,
  IN p_message_type ENUM('text','image','file','system'),
  IN p_is_temp TINYINT(1),
  IN p_created_at DATETIME
)
DELIMITER $$
BEGIN
  DECLARE v_sys_id INT UNSIGNED DEFAULT 99999;
  DECLARE v_day DATE;
  DECLARE v_label VARCHAR(64);
  DECLARE v_created DATETIME;

  -- created_at fallback
  SET v_created = IFNULL(p_created_at, NOW());
  SET v_day = DATE(v_created);
  SET v_label = CONCAT('--- ', DATE_FORMAT(v_day, '%Y-%m-%d'), ' ---');

  -- 1) Only auto insert day separator if this is NOT a system message
  IF p_message_type <> 'system' THEN

    -- If no day separator exists for that room+day -> insert it
	 IF NOT EXISTS (
		  SELECT 1
		  FROM messages m
		  WHERE m.room_id = p_room_id
			AND m.sender_id = v_sys_id
			AND m.message_type COLLATE utf8mb4_unicode_ci = 'system' COLLATE utf8mb4_unicode_ci
			AND m.content       COLLATE utf8mb4_unicode_ci = v_label  COLLATE utf8mb4_unicode_ci
			AND DATE(m.created_at) = v_day
		  LIMIT 1
		) THEN
      INSERT INTO messages (room_id, sender_id, content, message_type, is_temp, created_at)
      VALUES (p_room_id, v_sys_id, v_label, 'system', 0, TIMESTAMP(v_day));
    END IF;

  END IF;

  -- 2) Insert the real message
  INSERT INTO messages (room_id, sender_id, content, message_type, is_temp, created_at)
  VALUES (p_room_id, p_sender_id, p_content, p_message_type, IFNULL(p_is_temp, 0), v_created);

END
DELIMITER ;

CREATE DEFINER=`root`@`localhost` TRIGGER `trg_messages_after_insert` AFTER INSERT ON `messages` FOR EACH ROW BEGIN
    UPDATE rooms
    SET updated_at = NEW.created_at
    WHERE id = NEW.room_id;
END