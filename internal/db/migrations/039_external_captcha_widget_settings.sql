-- 外部人机验证 widget 仅用于普通用户场景；B/D 注册发码仍由本地强制图片验证码保护。
INSERT INTO settings(key,value) VALUES
 ('captcha_provider',''),
 ('external_captcha_register_enabled','0'),
 ('external_captcha_login_enabled','0'),
 ('external_captcha_phone_login_code_enabled','0'),
 ('captcha_geetest_id',''),
 ('captcha_vaptcha_vid',''),
 ('captcha_corptcha_site_key','')
ON CONFLICT(key) DO NOTHING;
