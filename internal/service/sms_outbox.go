package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"
)

type smsPayload struct {
	Template  SMSTemplate       `json:"template"`
	Preview   SMSPreview        `json:"preview"`
	SignName  string            `json:"sign_name"`
	Variables map[string]string `json:"variables"`
}
type smsJob struct {
	id, userID, templateID int64
	scene, recipient       string
	routeFingerprint       string
	payload                smsPayload
}

// 同一站内通知仅入队一次；不同业务调用生成不同通知ID，不能保证业务级恰好一次。
func (n *Notifier) enqueueSMS(ctx context.Context, tx *sql.Tx, notificationID, userID int64, code string, values map[string]string) error {
	scene, err := smsScene(code)
	if err != nil || scene.Kind != "notification" {
		return errors.New("短信通知场景无效")
	}
	var templateID int64
	var enabled bool
	err = tx.QueryRowContext(ctx, `SELECT coalesce(template_id,0),enabled FROM sms_scene_bindings WHERE code=$1`, code).Scan(&templateID, &enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.New("读取短信绑定失败")
	}
	if templateID == 0 {
		return nil
	}
	var phone string
	err = tx.QueryRowContext(ctx, `SELECT phone_e164 FROM users WHERE id=$1 AND status=1 AND phone_verified_at IS NOT NULL AND phone_e164 IS NOT NULL`, userID).Scan(&phone)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return errors.New("读取短信接收账户失败")
	}
	t, err := scanSMSTemplate(tx.QueryRowContext(ctx, `SELECT `+smsTemplateColumns+` FROM sms_templates WHERE id=$1`, templateID))
	if err != nil {
		return errors.New("读取短信模板失败")
	}
	route, err := n.loadSMSRoute(ctx, tx, t.RangeType)
	if err != nil {
		return err
	}
	var sign, site string
	if tx.QueryRowContext(ctx, `SELECT coalesce((SELECT value FROM settings WHERE key='site_name'),'')`).Scan(&site) != nil {
		return errors.New("读取短信设置失败")
	}
	sign = route.Config["sms_sign_name"]
	if strings.TrimSpace(site) == "" {
		site = DefaultSiteName
	}
	snapshot := map[string]string{}
	// 只保存该场景声明的变量，绝不把调用方附加的敏感字段存进短信快照。
	for _, v := range scene.Variables {
		if value, ok := values[v.Key]; ok {
			snapshot[v.Key] = value
		}
	}
	snapshot["site_name"] = site
	if t.SignName != "" {
		sign = t.SignName
	}
	if t.Provider == "submail" && t.RangeType == SMSRangeGlobal && t.SignName == "" {
		sign = route.Config["sms_global_sign_name"]
	}
	payload := smsPayload{Template: t, SignName: strings.Trim(strings.TrimSpace(sign), "【】"), Variables: snapshot}
	status, message := "pending", "等待发送"
	switch {
	case smsTemplateSendable(t) != nil:
		status, message = "skipped", "短信模板审核状态或远程编码不允许发送"
	case !enabled || !t.Enabled:
		status, message = "skipped", "场景或模板已停用，未发送"
	case t.Provider != route.Provider:
		status, message = "skipped", "模板与该范围当前短信服务商不匹配，未发送"
	default:
		preview, renderErr := renderSMSTemplate(t, scene, snapshot)
		if renderErr != nil || !smsTextValid(payload.SignName, 100) || payload.SignName == "" {
			status, message = "skipped", "模板变量或签名无效，未发送"
		} else {
			payload.Preview = preview
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return errors.New("短信快照编码失败")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO sms_outbox(notification_id,user_id,scene,template_id,recipient,payload,status,message,provider,range_type,remote_template_id,route_fingerprint) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(notification_id) DO NOTHING`, notificationID, userID, code, templateID, phone, string(raw), status, message, t.Provider, t.RangeType, t.TemplateCode, route.Fingerprint)
	if err != nil {
		return errors.New("短信通知意图保存失败")
	}
	return nil
}

// 发送中的租约失效只标记结果未知，永远不回到pending，避免收费请求被自动重复。
func (n *Notifier) claimSMS(ctx context.Context) (smsJob, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := n.db.ExecContext(ctx, `UPDATE sms_outbox SET status='unknown',message='发送中断，结果未知；不会自动重发',lease_until=NULL WHERE status='running' AND lease_until<=now()`); err != nil {
		return smsJob{}, err
	}
	var j smsJob
	var raw []byte
	err := n.db.QueryRowContext(ctx, `WITH candidate AS (SELECT id FROM sms_outbox WHERE status='pending' ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE sms_outbox o SET status='running',message='正在发送',attempt_count=attempt_count+1,lease_until=now()+interval '2 minutes' FROM candidate c WHERE o.id=c.id
 RETURNING o.id,o.user_id,o.template_id,o.scene,o.recipient,coalesce(o.route_fingerprint,''),o.payload`).Scan(&j.id, &j.userID, &j.templateID, &j.scene, &j.recipient, &j.routeFingerprint, &raw)
	if err != nil {
		return j, err
	}
	if json.Unmarshal(raw, &j.payload) != nil {
		return j, errors.New("短信载荷无效")
	}
	return j, nil
}
func (n *Notifier) smsJobAllowed(ctx context.Context, j smsJob) (bool, SMSRoute, error) {
	var allowed bool
	err := n.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sms_outbox o JOIN users u ON u.id=o.user_id
 JOIN sms_scene_bindings b ON b.code=o.scene JOIN sms_templates t ON t.id=b.template_id
 WHERE o.id=$1 AND o.status='running' AND o.lease_until>now() AND u.status=1 AND u.phone_verified_at IS NOT NULL
 AND u.phone_e164=$2 AND b.enabled AND t.enabled AND b.template_id=$3 AND t.kind='notification' AND t.provider=$4
 AND t.audit_status NOT IN ('pending','rejected') AND t.remote_operation='' AND t.range_type=coalesce(o.payload->'template'->>'range_type','cn')
 AND t.template_code=coalesce(o.payload->'template'->>'template_code',''))`, j.id, j.recipient, j.templateID, j.payload.Template.Provider).Scan(&allowed)
	if err != nil || !allowed {
		return allowed, SMSRoute{}, err
	}
	route, err := n.loadSMSRoute(ctx, n.db, j.payload.Template.RangeType)
	if err != nil {
		return false, SMSRoute{}, err
	}
	if j.routeFingerprint == "" || route.Provider != j.payload.Template.Provider || route.Fingerprint != j.routeFingerprint {
		return false, SMSRoute{}, nil
	}
	return true, route, nil
}

type smsDeliveryResult struct {
	status, message, providerMessageID, requestID, errorCode string
}

func smsResult(result SMSProviderResult, err error) smsDeliveryResult {
	if err == nil {
		return smsDeliveryResult{status: "sent", message: "供应商已接受发送", providerMessageID: result.ProviderMessageID, requestID: result.RequestID}
	}
	var unknown smsUnknownError
	if errors.As(err, &unknown) {
		return smsDeliveryResult{status: "unknown", message: "发送结果未知，不会自动重发", requestID: result.RequestID, errorCode: result.ProviderCode}
	}
	code := result.ProviderCode
	if code == "" {
		code = "provider_error"
	}
	return smsDeliveryResult{status: "failed", message: "配置无效或供应商拒绝，不会自动重发", requestID: result.RequestID, errorCode: code}
}
func (n *Notifier) finishSMS(ctx context.Context, j smsJob, result smsDeliveryResult) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := n.db.ExecContext(ctx, `UPDATE sms_outbox SET status=$2,message=$3,provider_message_id=$4,request_id=$5,error_code=$6,lease_until=NULL WHERE id=$1 AND status='running'`,
		j.id, result.status, result.message, result.providerMessageID, result.requestID, result.errorCode)
	return err
}
func (n *Notifier) deliverSMS(ctx context.Context, j smsJob) smsDeliveryResult {
	allowed, route, err := n.smsJobAllowed(ctx, j)
	if err != nil {
		return smsDeliveryResult{status: "skipped", message: "发送前复核失败，未发送", errorCode: "preflight_failed"}
	}
	if !allowed {
		return smsDeliveryResult{status: "skipped", message: "号码、账户、绑定或路由配置已变更，未发送", errorCode: "preflight_changed"}
	}
	scene, err := smsScene(j.scene)
	if err != nil {
		return smsDeliveryResult{status: "skipped", message: "短信场景无效，未发送", errorCode: "invalid_scene"}
	}
	preview, err := renderSMSTemplate(j.payload.Template, scene, j.payload.Variables)
	if err != nil {
		return smsDeliveryResult{status: "skipped", message: "短信快照无效，未发送", errorCode: "invalid_snapshot"}
	}
	j.payload.Preview = preview
	result, sendErr := (&ConfiguredSMSProvider{}).sendRenderedResult(ctx, j.recipient, preview, route.Config, false)
	return smsResult(result, sendErr)
}

// ponytail: 单实例固定一个短信worker，每次只有一次HTTP尝试；需要吞吐扩容时再引入供应商配额协调。
func (n *Notifier) StartSMS() {
	n.smsStartOnce.Do(func() {
		n.smsMu.Lock()
		defer n.smsMu.Unlock()
		if n.smsStopped {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		n.smsCancel = cancel
		n.smsDone = make(chan struct{})
		go func() {
			defer close(n.smsDone)
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				// ponytail: 单轮处理带 recover——适配器 panic 记日志后继续轮询；
				// StartSMS 只跑一次，worker 退出即队列永久停摆，故必须就地恢复。
				again := func() bool {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("短信发送单轮异常（已恢复，继续轮询）：%v", r)
						}
					}()
					j, err := n.claimSMS(ctx)
					if err == nil {
						sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
						result := n.deliverSMS(sendCtx, j)
						cancel()
						if n.finishSMS(context.Background(), j, result) != nil {
							log.Print("短信结果写回失败，租约到期后标记未知，不会自动重发")
						}
						return true
					}
					if !errors.Is(err, sql.ErrNoRows) && ctx.Err() == nil {
						log.Print("读取待发短信失败，稍后重新检查队列")
					}
					return false
				}()
				if again {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	})
}
func (n *Notifier) StopSMS(ctx context.Context) error {
	if n == nil {
		return nil
	}
	n.smsStopOnce.Do(func() {
		n.smsMu.Lock()
		defer n.smsMu.Unlock()
		n.smsStopped = true
		if n.smsCancel != nil {
			n.smsCancel()
		}
	})
	n.smsMu.Lock()
	done := n.smsDone
	n.smsMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("等待短信服务停止超时")
	}
}
func (n *Notifier) ListSMSDeliveries(ctx context.Context) ([]SMSDelivery, error) {
	rows, err := n.db.QueryContext(ctx, `SELECT id,scene,recipient,status,message,provider,provider_message_id,request_id,error_code,to_char(created_at,'YYYY-MM-DD HH24:MI:SS') FROM sms_outbox ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, errors.New("读取短信投递记录失败")
	}
	defer rows.Close()
	out := []SMSDelivery{}
	for rows.Next() {
		var d SMSDelivery
		if rows.Scan(&d.ID, &d.Scene, &d.Recipient, &d.Status, &d.Message, &d.Provider, &d.ProviderMessageID, &d.RequestID, &d.ErrorCode, &d.CreatedAt) != nil {
			return nil, errors.New("读取短信投递记录失败")
		}
		d.Recipient = MaskPhone(d.Recipient)
		out = append(out, d)
	}
	if rows.Err() != nil {
		return nil, errors.New("读取短信投递记录失败")
	}
	return out, nil
}
