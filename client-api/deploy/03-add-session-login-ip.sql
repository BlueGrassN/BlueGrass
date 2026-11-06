-- BlueGrass Client API / MySQL 8 / 已有会话表增加登录 IP。
-- 仅适用于已有 bg_client_sessions 且尚无 login_ip 的部署，执行一次。
-- 新部署使用 01-create-client-tables.sql，已含此字段，不执行本文件。
-- 由 DBA 执行；不向 v2_test 增加 ALTER 权限，不修改任何官网业务表。
-- 先升级表结构，再替换 API 程序；旧程序仍兼容新增的可空字段。
-- 已有会话保持 NULL，不猜测或回填历史 IP，也不清空或重建会话表。
-- 默认目标为生产库；仅在确认服务器、库名、表名和变更窗口后执行。
-- Hytron 测试库为 suntest，测试时须在私有副本中明确修改 USE。
-- MySQL 8.0.12+ 的适用 InnoDB 表可瞬时加列；若不支持则报错，
-- 不自动降级到复制整表。INSTANT 仍需短暂元数据锁，最多等待 5 秒。

USE `api_all_sunip_cc`;
SET SESSION lock_wait_timeout = 5;

ALTER TABLE `bg_client_sessions`
    ADD COLUMN login_ip VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NULL DEFAULT NULL,
    ALGORITHM=INSTANT;
