CREATE TABLE IF NOT EXISTS promotion_ending_notifications (
    promotion_id BIGINT NOT NULL REFERENCES promotions(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    notified_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (promotion_id, user_id)
);

ALTER TABLE sms_scene_bindings DROP CONSTRAINT IF EXISTS sms_scene_bindings_code_check;
ALTER TABLE sms_scene_bindings ADD CONSTRAINT sms_scene_bindings_code_check CHECK (code IN (
    'otp_register','otp_login','otp_reset_password','otp_bind','otp_change','otp_profile_phone_old','otp_verify_phone',
    'ticket_created','ticket_reply','ticket_assigned','ticket_timeout','payment_success','order_submitted','recharge_success','service_expiring','promotion_ending',
    'identity_submitted','identity_approved','identity_rejected','cancel_submitted','cancel_approved','cancel_rejected'
));
