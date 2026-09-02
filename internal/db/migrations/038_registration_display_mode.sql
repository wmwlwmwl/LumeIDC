-- 注册展示策略：0=二选一，1=邮箱/手机号两个注册表单同时展示。
INSERT INTO settings(key,value) VALUES ('registration_show_all_methods','0')
ON CONFLICT(key) DO NOTHING;
