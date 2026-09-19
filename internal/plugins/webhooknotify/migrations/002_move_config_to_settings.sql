-- webhooknotify 002：一期自管配置表 → settings（键 plugin.webhooknotify.*）。
-- 旧表保留不删（无害回滚线索）；已存在同名 settings 键时跳过（不覆盖新配置）。
INSERT INTO settings(key, value)
  SELECT 'plugin.webhooknotify.url', url FROM plugin_webhooknotify_config WHERE id=1 AND url<>''
  ON CONFLICT (key) DO NOTHING;
INSERT INTO settings(key, value)
  SELECT 'plugin.webhooknotify.secret', secret FROM plugin_webhooknotify_config WHERE id=1 AND secret<>''
  ON CONFLICT (key) DO NOTHING;
INSERT INTO settings(key, value)
  SELECT 'plugin.webhooknotify.events', events::text FROM plugin_webhooknotify_config WHERE id=1
  ON CONFLICT (key) DO NOTHING;
INSERT INTO settings(key, value)
  SELECT 'plugin.webhooknotify.enabled', CASE WHEN enabled THEN '1' ELSE '0' END
  FROM plugin_webhooknotify_config WHERE id=1
  ON CONFLICT (key) DO NOTHING;
