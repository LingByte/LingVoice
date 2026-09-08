-- LingVoice rollback initial migration
-- Drop all tables
-- Warning: this will lose data, confirm before running
DROP TABLE IF EXISTS `role_permissions`;
DROP TABLE IF EXISTS `user_roles`;
DROP TABLE IF EXISTS `permissions`;
DROP TABLE IF EXISTS `roles`;
DROP TABLE IF EXISTS `tenants`;
DROP TABLE IF EXISTS `users`;
