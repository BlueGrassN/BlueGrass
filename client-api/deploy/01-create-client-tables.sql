-- BlueGrass Client API / MySQL 8 / InnoDB
-- 在现有数据库中新建 3 张 API 会话表，不新建数据库，不修改官网业务表。
-- 1. bg_client_sessions        登录会话、登录 IP、访问令牌哈希、有效期和撤销状态。
-- 2. bg_client_refresh_tokens  刷新令牌轮换、重用检测和重试恢复。
-- 3. bg_client_device_sessions 同设备登录与当前会话关联。
--
-- 已确认生产数据库名为 api_all_sunip_cc；执行前核对目标服务器和数据库。
-- 本文件由数据库管理员执行；运行账号 v2_test 不需要建表权限。
-- 默认 session_prefix 为 bg_client_；若修改，所有表名、外键和授权必须同步修改。
-- 已有同名表时先核对结构；IF NOT EXISTS 不会升级或修复已有结构。
-- 旧版缺少 login_ip 时，由 DBA 单独执行 03-add-session-login-ip.sql。
-- 与 internal/session/store.go 中的 Migrate 建表结构保持一致。

USE `api_all_sunip_cc`;

CREATE TABLE IF NOT EXISTS `bg_client_sessions` (
    id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin PRIMARY KEY,
    customer_id BIGINT NOT NULL, device_hash BINARY(32) NOT NULL, device_name VARCHAR(120) NOT NULL,
    credential_fingerprint BINARY(32) NOT NULL, access_hash BINARY(32) NOT NULL,
    access_expires_at DATETIME(6) NOT NULL, idle_expires_at DATETIME(6) NOT NULL,
    generation BIGINT NOT NULL DEFAULT 0, revoked_at DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
    login_ip VARCHAR(45) CHARACTER SET ascii COLLATE ascii_bin NULL DEFAULT NULL,
    UNIQUE KEY(access_hash), KEY(customer_id,device_hash), KEY(idle_expires_at), KEY(revoked_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `bg_client_refresh_tokens` (
    token_hash BINARY(32) PRIMARY KEY,
    session_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    generation BIGINT NOT NULL, used_at DATETIME(6) NULL, request_hash BINARY(32) NULL,
    retry_cipher BLOB NULL, retry_expires_at DATETIME(6) NULL, created_at DATETIME(6) NOT NULL,
    KEY(session_id), KEY(retry_expires_at), KEY(used_at),
    FOREIGN KEY(session_id) REFERENCES `bg_client_sessions`(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `bg_client_device_sessions` (
    customer_id BIGINT NOT NULL, device_hash BINARY(32) NOT NULL,
    session_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NULL, updated_at DATETIME(6) NOT NULL,
    PRIMARY KEY(customer_id,device_hash), KEY(session_id),
    FOREIGN KEY(session_id) REFERENCES `bg_client_sessions`(id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
