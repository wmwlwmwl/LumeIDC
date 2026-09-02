package repo

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrChallengeInvalid = errors.New("验证码无效或已过期")
	ErrChallengeCode    = errors.New("验证码错误")
	ErrPhoneInUse       = errors.New("手机号不可用")
	ErrVerificationBusy = errors.New("已有实名申请正在审核")
	ErrVerificationDone = errors.New("实名已通过，不能重复提交")
	ErrNotPending       = errors.New("该申请已处理")
)

type IdentityStore struct{ db *sql.DB }

type RealNameSubmission struct {
	ID                       int64
	UserID                   int64
	Email                    string
	Phone                    string
	Status                   string
	Source                   string
	ProviderRef              string
	LegalNameCiphertext      string
	IdentityNumberCiphertext string
	IdentityNumberHMAC       string
	FrontPhotoRef            string
	BackPhotoRef             string
	SubmittedAt              time.Time
	ReviewedAt               sql.NullTime
	ReviewedBy               sql.NullInt64
	RejectionReason          string
	Version                  int
}

type AutomaticIdentityAttempt struct {
	ID                       int64
	UserID                   int64
	Email                    string
	Phone                    string
	ProviderKey              string
	ProviderRef              string
	ProviderURL              string
	Status                   string
	LegalNameCiphertext      string
	IdentityNumberCiphertext string
	IdentityNumberHMAC       string
	SubmittedAt              time.Time
	CompletedAt              sql.NullTime
	FailureMessage           string
	Version                  int
}

type RealNameSummary struct {
	ID          int64
	UserID      int64
	Email       string
	Phone       string
	Status      string
	SubmittedAt time.Time
}

func (s *IdentityStore) UserPhone(ctx context.Context, userID int64) (phone string, verified bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT coalesce(phone_e164,''), phone_verified_at IS NOT NULL FROM users WHERE id=$1 AND status=1`, userID).Scan(&phone, &verified)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

// PendingPhone 当前未消费/未作废挑战中的目标手机号；无记录返回 sql.ErrNoRows。
func (s *IdentityStore) PendingPhone(ctx context.Context, userID int64, purpose string) (string, error) {
	var target string
	err := s.db.QueryRowContext(ctx, `SELECT phone_e164 FROM phone_verification_challenges WHERE user_id=$1 AND purpose=$2 AND consumed_at IS NULL AND invalidated_at IS NULL ORDER BY id DESC LIMIT 1`, userID, purpose).Scan(&target)
	return target, err
}

func (s *IdentityStore) CreatePhoneChallenge(ctx context.Context, userID int64, purpose, phone, codeHMAC, ip string, expiresAt, now time.Time) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var status int16
	var current sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT status,phone_e164 FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&status, &current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if status == 0 {
		return 0, ErrDisabled
	}
	if purpose == "bind" && current.Valid && current.String != "" {
		return 0, errors.New("账户已绑定手机号，请使用换绑流程")
	}
	if purpose == "change" && (!current.Valid || current.String == "") {
		return 0, errors.New("账户尚未绑定手机号")
	}
	if purpose == "login" && (!current.Valid || current.String != phone) {
		return 0, errors.New("手机号不可用")
	}
	var used bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE phone_e164=$1 AND id<>$2)`, phone, userID).Scan(&used); err != nil {
		return 0, err
	}
	if used {
		return 0, ErrPhoneInUse
	}
	var recent int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM phone_verification_challenges WHERE user_id=$1 AND created_at>$2`, userID, now.Add(-time.Minute)).Scan(&recent); err != nil {
		return 0, err
	}
	if recent > 0 {
		return 0, errors.New("验证码发送过于频繁，请稍后再试")
	}
	var hourly int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM phone_verification_challenges WHERE (user_id=$1 OR phone_e164=$2 OR request_ip=$3) AND created_at>$4`, userID, phone, ip, now.Add(-time.Hour)).Scan(&hourly); err != nil {
		return 0, err
	}
	if hourly >= 10 {
		return 0, errors.New("验证码发送次数已达上限，请稍后再试")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET invalidated_at=$2 WHERE user_id=$1 AND purpose=$3 AND consumed_at IS NULL AND invalidated_at IS NULL`, userID, now, purpose); err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO phone_verification_challenges(user_id,purpose,phone_e164,code_hmac,expires_at,request_ip) VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, userID, purpose, phone, codeHMAC, expiresAt, ip).Scan(&id); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *IdentityStore) InvalidatePhoneChallenge(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE phone_verification_challenges SET invalidated_at=$2 WHERE id=$1 AND consumed_at IS NULL`, id, at)
	return err
}

func (s *IdentityStore) ConsumePhoneChallenge(ctx context.Context, userID int64, purpose, codeHMAC string, now time.Time) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var id int64
	var phone, saved string
	var expires time.Time
	var attempts int
	if err := tx.QueryRowContext(ctx, `SELECT id,phone_e164,code_hmac,expires_at,attempts FROM phone_verification_challenges WHERE user_id=$1 AND purpose=$2 AND consumed_at IS NULL AND invalidated_at IS NULL ORDER BY id DESC LIMIT 1 FOR UPDATE`, userID, purpose).Scan(&id, &phone, &saved, &expires, &attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrChallengeInvalid
		}
		return "", err
	}
	if attempts >= 5 || !expires.After(now) {
		_, _ = tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET invalidated_at=$2 WHERE id=$1`, id, now)
		_ = tx.Commit()
		return "", ErrChallengeInvalid
	}
	if !hmac.Equal([]byte(saved), []byte(codeHMAC)) {
		if attempts+1 >= 5 {
			_, _ = tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET attempts=attempts+1,invalidated_at=$2 WHERE id=$1`, id, now)
		} else {
			_, _ = tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET attempts=attempts+1 WHERE id=$1`, id)
		}
		if err := tx.Commit(); err != nil {
			return "", err
		}
		return "", ErrChallengeCode
	}
	var status int16
	if err := tx.QueryRowContext(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&status); err != nil {
		return "", err
	}
	if status == 0 {
		return "", ErrDisabled
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET phone_e164=$2,phone_verified_at=$3,phone_updated_at=$3 WHERE id=$1`, userID, phone, now); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET consumed_at=$2 WHERE id=$1`, id, now); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET invalidated_at=$2 WHERE user_id=$1 AND purpose=$3 AND id<>$4 AND consumed_at IS NULL AND invalidated_at IS NULL`, userID, now, purpose, id); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return phone, nil
}

func (s *IdentityStore) ConsumeLoginPhoneChallenge(ctx context.Context, userID int64, codeHMAC string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id int64
	var saved string
	var expires time.Time
	var attempts int
	if err := tx.QueryRowContext(ctx, `SELECT id,code_hmac,expires_at,attempts FROM phone_verification_challenges WHERE user_id=$1 AND purpose='login' AND consumed_at IS NULL AND invalidated_at IS NULL ORDER BY id DESC LIMIT 1 FOR UPDATE`, userID).Scan(&id, &saved, &expires, &attempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrChallengeInvalid
		}
		return err
	}
	if attempts >= 5 || !expires.After(now) {
		_, _ = tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET invalidated_at=$2 WHERE id=$1`, id, now)
		_ = tx.Commit()
		return ErrChallengeInvalid
	}
	if !hmac.Equal([]byte(saved), []byte(codeHMAC)) {
		if attempts+1 >= 5 {
			_, _ = tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET attempts=attempts+1,invalidated_at=$2 WHERE id=$1`, id, now)
		} else {
			_, _ = tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET attempts=attempts+1 WHERE id=$1`, id)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return ErrChallengeCode
	}
	if _, err := tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET consumed_at=$2 WHERE id=$1`, id, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *IdentityStore) PhoneTaken(ctx context.Context, phone string, excludeID int64) (bool, error) {
	var taken bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE phone_e164=$1 AND id<>$2)`, phone, excludeID).Scan(&taken)
	return taken, err
}

func (s *IdentityStore) AdminSetPhone(ctx context.Context, userID int64, phone string, now time.Time) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var old sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT phone_e164 FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	oldPhone := ""
	if old.Valid {
		oldPhone = old.String
	}
	if oldPhone == phone {
		return false, tx.Commit()
	}
	if phone != "" {
		var used bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE phone_e164=$1 AND id<>$2)`, phone, userID).Scan(&used); err != nil {
			return false, err
		}
		if used {
			return false, ErrPhoneInUse
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE phone_verification_challenges SET invalidated_at=$2 WHERE user_id=$1 AND consumed_at IS NULL AND invalidated_at IS NULL`, userID, now); err != nil {
		return false, err
	}
	var updateErr error
	if phone == "" {
		_, updateErr = tx.ExecContext(ctx, `UPDATE users SET phone_e164=NULL,phone_verified_at=NULL,phone_updated_at=$2 WHERE id=$1`, userID, now)
	} else {
		_, updateErr = tx.ExecContext(ctx, `UPDATE users SET phone_e164=$2,phone_verified_at=NULL,phone_updated_at=$3 WHERE id=$1`, userID, phone, now)
	}
	if updateErr != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// CurrentVerification is retained as a compatibility name and returns the current manual submission only.
func (s *IdentityStore) CurrentVerification(ctx context.Context, userID int64) (*RealNameSubmission, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id,user_id,status,'manual',legal_name_ciphertext,identity_number_ciphertext,identity_number_hmac,front_photo_ref,back_photo_ref,submitted_at,reviewed_at,reviewed_by,rejection_reason,version FROM manual_identity_submissions WHERE user_id=$1 ORDER BY CASE status WHEN 'pending' THEN 0 WHEN 'approved' THEN 1 ELSE 2 END,id DESC LIMIT 1`, userID)
	var v RealNameSubmission
	if err := row.Scan(&v.ID, &v.UserID, &v.Status, &v.Source, &v.LegalNameCiphertext, &v.IdentityNumberCiphertext, &v.IdentityNumberHMAC, &v.FrontPhotoRef, &v.BackPhotoRef, &v.SubmittedAt, &v.ReviewedAt, &v.ReviewedBy, &v.RejectionReason, &v.Version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

func (s *IdentityStore) CreateSubmission(ctx context.Context, userID int64, legalName, identityCipher, identityHMAC, frontRef, backRef string, now time.Time, requirePhone bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var phoneVerified bool
	if err := tx.QueryRowContext(ctx, `SELECT status=1 AND phone_verified_at IS NOT NULL FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&phoneVerified); err != nil {
		return err
	}
	if requirePhone && !phoneVerified {
		return errors.New("请先完成手机号验证")
	}
	var status string
	err = tx.QueryRowContext(ctx, `SELECT status FROM manual_identity_submissions WHERE user_id=$1 ORDER BY CASE status WHEN 'pending' THEN 0 WHEN 'approved' THEN 1 ELSE 2 END,id DESC LIMIT 1 FOR UPDATE`, userID).Scan(&status)
	if err == nil {
		if status == "pending" {
			return ErrVerificationBusy
		}
		if status == "approved" {
			return ErrVerificationDone
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var duplicate bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manual_identity_submissions WHERE identity_number_hmac=$1 AND status IN ('pending','approved'))`, identityHMAC).Scan(&duplicate); err != nil {
		return err
	}
	if duplicate {
		return errors.New("该证件已被其他账号使用")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO manual_identity_submissions(user_id,legal_name_ciphertext,identity_number_ciphertext,identity_number_hmac,front_photo_ref,back_photo_ref,submitted_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$7,$7)`, userID, legalName, identityCipher, identityHMAC, frontRef, backRef, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *IdentityStore) CreatePluginSubmission(ctx context.Context, userID int64, source, providerRef, providerURL, legalName, identityCipher, identityHMAC string, now time.Time) (int64, error) {
	if source == "" || source == "manual" || providerRef == "" {
		return 0, errors.New("实名 provider 参数无效")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var status int16
	if err := tx.QueryRowContext(ctx, `SELECT status FROM users WHERE id=$1 FOR UPDATE`, userID).Scan(&status); err != nil {
		return 0, err
	}
	if status == 0 {
		return 0, ErrDisabled
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automatic_identity_attempts WHERE user_id=$1 AND status IN ('initiated','pending'))`, userID).Scan(&pending); err != nil {
		return 0, err
	}
	if pending {
		return 0, ErrVerificationBusy
	}
	var approved bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM automatic_identity_attempts WHERE user_id=$1 AND status='approved')`, userID).Scan(&approved); err != nil {
		return 0, err
	}
	if approved {
		return 0, ErrVerificationDone
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO automatic_identity_attempts(user_id,provider_key,provider_ref,provider_url,status,legal_name_ciphertext,identity_number_ciphertext,identity_number_hmac,submitted_at,created_at,updated_at) VALUES($1,$2,$3,$4,'pending',$5,$6,$7,$8,$8,$8) RETURNING id`, userID, source, providerRef, providerURL, legalName, identityCipher, identityHMAC, now).Scan(&id); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *IdentityStore) HasApproved(ctx context.Context, userID int64) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM manual_identity_submissions WHERE user_id=$1 AND status='approved') OR EXISTS(SELECT 1 FROM automatic_identity_attempts WHERE user_id=$1 AND status='approved')`, userID).Scan(&ok)
	return ok, err
}

func (s *IdentityStore) UpdateProviderStatus(ctx context.Context, userID int64, providerRef, status, message string, now time.Time) error {
	if status != "pending" && status != "approved" && status != "rejected" && status != "failed" && status != "expired" {
		return errors.New("实名状态无效")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE automatic_identity_attempts SET status=$1,failure_message=$2,completed_at=CASE WHEN $1 IN ('approved','rejected','failed','expired') THEN $5 ELSE NULL END,updated_at=$5,version=version+1 WHERE user_id=$3 AND provider_ref=$4 AND status IN ('initiated','pending')`, status, message, userID, providerRef, now)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *IdentityStore) AutomaticAttempt(ctx context.Context, userID, id int64) (*AutomaticIdentityAttempt, error) {
	row := s.db.QueryRowContext(ctx, `SELECT a.id,a.user_id,coalesce(u.email,''),coalesce(u.phone_e164,''),a.provider_key,a.provider_ref,a.provider_url,a.status,a.legal_name_ciphertext,a.identity_number_ciphertext,a.identity_number_hmac,a.submitted_at,a.completed_at,a.failure_message,a.version FROM automatic_identity_attempts a JOIN users u ON u.id=a.user_id WHERE a.id=$1 AND a.user_id=$2`, id, userID)
	var a AutomaticIdentityAttempt
	if err := row.Scan(&a.ID, &a.UserID, &a.Email, &a.Phone, &a.ProviderKey, &a.ProviderRef, &a.ProviderURL, &a.Status, &a.LegalNameCiphertext, &a.IdentityNumberCiphertext, &a.IdentityNumberHMAC, &a.SubmittedAt, &a.CompletedAt, &a.FailureMessage, &a.Version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (s *IdentityStore) CurrentAutomatic(ctx context.Context, userID int64) (*AutomaticIdentityAttempt, error) {
	row := s.db.QueryRowContext(ctx, `SELECT a.id,a.user_id,coalesce(u.email,''),coalesce(u.phone_e164,''),a.provider_key,a.provider_ref,a.provider_url,a.status,a.legal_name_ciphertext,a.identity_number_ciphertext,a.identity_number_hmac,a.submitted_at,a.completed_at,a.failure_message,a.version FROM automatic_identity_attempts a JOIN users u ON u.id=a.user_id WHERE a.user_id=$1 ORDER BY CASE a.status WHEN 'pending' THEN 0 WHEN 'initiated' THEN 1 WHEN 'approved' THEN 2 ELSE 3 END,a.id DESC LIMIT 1`, userID)
	var a AutomaticIdentityAttempt
	if err := row.Scan(&a.ID, &a.UserID, &a.Email, &a.Phone, &a.ProviderKey, &a.ProviderRef, &a.ProviderURL, &a.Status, &a.LegalNameCiphertext, &a.IdentityNumberCiphertext, &a.IdentityNumberHMAC, &a.SubmittedAt, &a.CompletedAt, &a.FailureMessage, &a.Version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (s *IdentityStore) PendingList(ctx context.Context, limit int) ([]RealNameSummary, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT v.id,v.user_id,coalesce(u.email,''),coalesce(u.phone_e164,''),v.status,v.submitted_at FROM manual_identity_submissions v JOIN users u ON u.id=v.user_id WHERE v.status='pending' ORDER BY v.submitted_at ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RealNameSummary
	for rows.Next() {
		var v RealNameSummary
		if err := rows.Scan(&v.ID, &v.UserID, &v.Email, &v.Phone, &v.Status, &v.SubmittedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *IdentityStore) Submission(ctx context.Context, id int64) (*RealNameSubmission, error) {
	row := s.db.QueryRowContext(ctx, `SELECT v.id,v.user_id,coalesce(u.email,''),coalesce(u.phone_e164,''),v.status,'manual', '',v.legal_name_ciphertext,v.identity_number_ciphertext,v.identity_number_hmac,v.front_photo_ref,v.back_photo_ref,v.submitted_at,v.reviewed_at,v.reviewed_by,v.rejection_reason,v.version FROM manual_identity_submissions v JOIN users u ON u.id=v.user_id WHERE v.id=$1`, id)
	var v RealNameSubmission
	if err := row.Scan(&v.ID, &v.UserID, &v.Email, &v.Phone, &v.Status, &v.Source, &v.ProviderRef, &v.LegalNameCiphertext, &v.IdentityNumberCiphertext, &v.IdentityNumberHMAC, &v.FrontPhotoRef, &v.BackPhotoRef, &v.SubmittedAt, &v.ReviewedAt, &v.ReviewedBy, &v.RejectionReason, &v.Version); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &v, nil
}

func (s *IdentityStore) Review(ctx context.Context, id, adminID int64, approve bool, reason string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM manual_identity_submissions WHERE id=$1 FOR UPDATE`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if status != "pending" {
		return ErrNotPending
	}
	newStatus := "rejected"
	if approve {
		newStatus = "approved"
		reason = ""
	}
	if !approve && reason == "" {
		return errors.New("拒绝原因不能为空")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE manual_identity_submissions SET status=$2,reviewed_at=$3,reviewed_by=$4,rejection_reason=$5,version=version+1,updated_at=$3 WHERE id=$1`, id, newStatus, now, adminID, reason); err != nil {
		return err
	}
	return tx.Commit()
}
