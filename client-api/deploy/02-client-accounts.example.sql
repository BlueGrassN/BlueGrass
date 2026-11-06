-- BlueGrass Client API / MySQL 8 / 同库单账号 v2_test 的创建和授权示例。
-- 先完成 01-create-client-tables.sql；本文件由数据库管理员执行。
-- 仅使用 1 个运行账号，不新建表或数据库。
-- source.user 和 sessions.user 均为 v2_test，连接 127.0.0.1:3306。
-- 库级 SELECT 允许读取其他表；写权限仅逐表授予 API 自己的三张会话表。
--
-- 执行前替换所有占位符：
-- REPLACE_DATABASE_PASSWORD：运行账号密码，两个连接池填写相同值。
-- 生产数据库名已确认为 api_all_sunip_cc；执行前核对目标服务器。
-- 密码含 SQL 特殊字符时须正确转义，或通过宝塔/安全管理工具创建账号。
-- 含真实密码的文件不得提交 Git、放入网站目录或共享日志。
-- 账号 Host 示例固定为 127.0.0.1，限定本机 TCP 连接，不开放 %。
-- 若 v2_test 已存在，跳过 CREATE USER，先核对已有账号的实际 Host 和角色。
-- 若现有账户是 v2_test@localhost，应将下面全部 Host 改为 localhost，
-- 对该既有账户授权，不另外创建第二个不同 Host 的同名账号。
-- GRANT 只增加权限，不移除已有权限；既有账号不能仅靠执行本文件实现降权。

CREATE USER 'v2_test'@'127.0.0.1'
    IDENTIFIED BY 'REPLACE_DATABASE_PASSWORD';

-- 全库只读权限，不授予库级 INSERT / UPDATE / DELETE 或 DDL 权限。
GRANT SELECT ON `api_all_sunip_cc`.*
    TO 'v2_test'@'127.0.0.1';

-- API 自己的三张表：追加当前实现需要的表级读写权限。
GRANT SELECT, INSERT, UPDATE, DELETE ON `api_all_sunip_cc`.`bg_client_sessions`
    TO 'v2_test'@'127.0.0.1';
GRANT SELECT, INSERT, UPDATE, DELETE ON `api_all_sunip_cc`.`bg_client_refresh_tokens`
    TO 'v2_test'@'127.0.0.1';
GRANT SELECT, INSERT, UPDATE ON `api_all_sunip_cc`.`bg_client_device_sessions`
    TO 'v2_test'@'127.0.0.1';

SHOW GRANTS FOR 'v2_test'@'127.0.0.1';
