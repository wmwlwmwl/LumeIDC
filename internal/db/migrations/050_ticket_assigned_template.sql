INSERT INTO settings(key,value) VALUES
  ('ticket_notify_assigned_body','你的工单「{{subject}}」已分配客服处理。')
ON CONFLICT (key) DO NOTHING;
