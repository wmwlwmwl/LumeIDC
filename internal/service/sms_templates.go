package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type SMSTemplate struct {
	ID               int64             `json:"id"`
	Name             string            `json:"name"`
	Provider         string            `json:"provider"`
	Kind             string            `json:"kind"`
	TemplateCode     string            `json:"template_code"`
	Content          string            `json:"content"`
	Parameters       map[string]string `json:"parameters"`
	Enabled          bool              `json:"enabled"`
	RangeType        SMSRange          `json:"range_type"`
	RemoteTemplateID string            `json:"remote_template_id"`
	AuditStatus      SMSAuditStatus    `json:"audit_status"`
	AuditMessage     string            `json:"audit_message"`
	AuditUpdatedAt   *time.Time        `json:"audit_updated_at,omitempty"`
	Remark           string            `json:"remark"`
	SignName         string            `json:"sign_name"`
	TemplateType     string            `json:"template_type"`
	RemoteOperation  string            `json:"remote_operation"`
}
type SMSScene struct {
	Code       string                  `json:"code"`
	Name       string                  `json:"name"`
	Kind       string                  `json:"kind"`
	Required   bool                    `json:"required"`
	Variables  []EmailTemplateVariable `json:"variables"`
	TemplateID int64                   `json:"template_id"`
	Enabled    bool                    `json:"enabled"`
}
type SMSBinding struct {
	Code       string `json:"code"`
	TemplateID int64  `json:"template_id"`
	Enabled    bool   `json:"enabled"`
}
type SMSPreviewRequest struct {
	Template SMSTemplate `json:"template"`
	Scene    string      `json:"scene"`
}
type SMSPreview struct {
	Content      string            `json:"content"`
	TemplateCode string            `json:"template_code"`
	Parameters   map[string]string `json:"parameters"`
	Provider     string            `json:"provider"`
	RangeType    SMSRange          `json:"range_type"`
	SignName     string            `json:"sign_name"`
}
type SMSDelivery struct {
	ID                int64  `json:"id"`
	Scene             string `json:"scene"`
	Recipient         string `json:"recipient"`
	Status            string `json:"status"`
	Message           string `json:"message"`
	Provider          string `json:"provider"`
	ProviderMessageID string `json:"provider_message_id"`
	RequestID         string `json:"request_id"`
	ErrorCode         string `json:"error_code"`
	CreatedAt         string `json:"created_at"`
}

func defaultSMSScenes() []SMSScene {
	out := make([]SMSScene, 0, 20)
	for _, item := range []struct{ purpose, name string }{{"register", "注册验证码"}, {"login", "登录验证码"}, {"reset_password", "重置密码验证码"}, {"bind", "绑定手机验证码"}, {"change", "换绑手机验证码"}, {"profile_phone_old", "原手机号验证码"}, {"verify_phone", "手机验证验证码"}} {
		out = append(out, SMSScene{Code: "otp_" + item.purpose, Name: item.name, Kind: "otp", Required: true, Enabled: true, Variables: []EmailTemplateVariable{{"code", "验证码", "123456"}, {"purpose", "验证码用途", item.purpose}, {"sign_name", "短信签名", "示例站点"}, {"site_name", "站点名称", DefaultSiteName}}})
	}
	for _, t := range defaultEmailTemplates() {
		if t.Code != "auth_code" {
			out = append(out, SMSScene{Code: t.Code, Name: t.Name, Kind: "notification", Variables: t.Variables})
		}
	}
	return out
}
func smsScene(code string) (SMSScene, error) {
	for _, s := range defaultSMSScenes() {
		if s.Code == code {
			return s, nil
		}
	}
	return SMSScene{}, errors.New("短信场景不存在")
}
func smsTextValid(s string, limit int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= limit && strings.IndexFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) < 0
}

var smsIdentifier = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
var smsPlaceholder = regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}|@var\(([A-Za-z0-9_]+)\)|\$\{([A-Za-z0-9_]+)\}|\{([A-Za-z0-9_]+)\}`)

func renderSMSTemplate(t SMSTemplate, scene SMSScene, values map[string]string) (SMSPreview, error) {
	if t.RangeType == "" {
		t.RangeType = SMSRangeCN
	}
	result := SMSPreview{Provider: t.Provider, TemplateCode: t.TemplateCode, Parameters: map[string]string{}, RangeType: t.RangeType, SignName: t.SignName}
	if t.Kind != scene.Kind || !smsProviderSupports(t.Provider, t.RangeType, t.Kind) {
		return result, errors.New("短信模板与服务商范围或场景类型不匹配")
	}
	if strings.TrimSpace(t.Name) == "" || !smsTextValid(t.Name, 100) || !smsTextValid(t.Content, 500) || !smsTextValid(t.Remark, 500) || !smsTextValid(t.SignName, 100) {
		return result, errors.New("短信模板字段含控制字符、过长或名称为空")
	}
	if t.TemplateCode != "" && !smsIdentifier.MatchString(t.TemplateCode) {
		return result, errors.New("短信模板编码无效")
	}
	allowed := map[string]bool{}
	for _, v := range scene.Variables {
		allowed[v.Key] = true
	}
	value := func(key string) (string, error) {
		v, ok := values[key]
		if !allowed[key] || !ok || strings.TrimSpace(v) == "" || !smsTextValid(v, 500) {
			return "", errors.New("短信变量未知、缺失、含控制字符或过长")
		}
		return v, nil
	}
	hasCode := false
	if len(t.Parameters) > 20 {
		return result, errors.New("短信参数不能超过20项")
	}
	for param, key := range t.Parameters {
		if !smsIdentifier.MatchString(param) {
			return result, errors.New("短信参数名无效")
		}
		if t.Provider == "qcloudsms" {
			i, e := strconv.Atoi(strings.TrimPrefix(param, "param"))
			if e != nil || i < 1 || i > len(t.Parameters) || strconv.Itoa(i) != strings.TrimPrefix(param, "param") {
				return result, errors.New("腾讯云参数须使用从1连续编号的数字或param编号")
			}
			for other := range t.Parameters {
				if other != param && strings.TrimPrefix(other, "param") == strings.TrimPrefix(param, "param") {
					return result, errors.New("腾讯云参数编号重复")
				}
			}
		}
		v, e := value(key)
		if e != nil {
			return result, e
		}
		result.Parameters[param] = v
		hasCode = hasCode || key == "code"
	}
	switch t.Provider {
	case "aliyun":
		if t.Content != "" || !smsIdentifier.MatchString(t.TemplateCode) || len(t.Parameters) > 1 || (len(t.Parameters) == 1 && t.Parameters["code"] != "code") {
			return result, errors.New("号码认证仅支持验证码模板编码与code映射")
		}
		v, e := value("code")
		if e != nil {
			return result, e
		}
		result.Parameters["code"] = v
		hasCode = true
	case "aliyun_sms", "qcloudsms":
		if t.TemplateCode == "" && t.Content == "" {
			return result, errors.New("请填写模板编码或待提交的模板正文")
		}
		if t.Content != "" {
			content, e := smsCloudContent(t)
			if e != nil {
				return result, e
			}
			result.Content = smsPlaceholder.ReplaceAllStringFunc(content, func(token string) string {
				m := smsPlaceholder.FindStringSubmatch(token)
				for _, key := range m[1:] {
					if key != "" {
						if t.Provider == "qcloudsms" {
							if v, ok := result.Parameters[key]; ok {
								return v
							}
							return result.Parameters["param"+key]
						}
						return result.Parameters[key]
					}
				}
				return token
			})
		}
	default:
		if strings.TrimSpace(t.Content) == "" {
			return result, errors.New("文本短信必须填写正文")
		}
		if (t.Provider == "stay33" || t.Provider == "smsbao") && (t.TemplateCode != "" || len(t.Parameters) != 0) {
			return result, errors.New("该文本短信不使用模板编码或参数映射")
		}
		var renderErr error
		result.Content = smsPlaceholder.ReplaceAllStringFunc(t.Content, func(token string) string {
			match := smsPlaceholder.FindStringSubmatch(token)
			key := ""
			for _, s := range match[1:] {
				if s != "" {
					key = s
				}
			}
			param := key
			if mapped, ok := t.Parameters[key]; ok {
				key = mapped
			}
			v, e := value(key)
			if e != nil {
				renderErr = e
				return ""
			}
			hasCode = hasCode || key == "code"
			result.Parameters[param] = v
			return v
		})
		residue := smsPlaceholder.ReplaceAllString(t.Content, "")
		if renderErr != nil {
			return result, renderErr
		}
		if strings.ContainsAny(residue, "{}") || strings.Contains(residue, "@var(") {
			return result, errors.New("短信变量格式错误")
		}
		if !smsTextValid(result.Content, 500) {
			return result, errors.New("短信渲染结果超过500字")
		}
	}
	if t.Kind == "otp" && !hasCode {
		return result, errors.New("验证码模板必须包含或映射code")
	}
	raw, _ := json.Marshal(result.Parameters)
	if len(raw) > 6000 {
		return result, errors.New("短信参数总长度过大")
	}
	return result, nil
}
func smsExamples(scene SMSScene) map[string]string {
	values := map[string]string{}
	for _, v := range scene.Variables {
		values[v.Key] = v.Example
	}
	return values
}
func validateSMSTemplate(t SMSTemplate) error {
	scene := SMSScene{Kind: t.Kind}
	seen := map[string]bool{}
	for _, s := range defaultSMSScenes() {
		if s.Kind == t.Kind {
			for _, v := range s.Variables {
				if !seen[v.Key] {
					scene.Variables = append(scene.Variables, v)
					seen[v.Key] = true
				}
			}
		}
	}
	_, e := renderSMSTemplate(t, scene, smsExamples(scene))
	return e
}
func (n *Notifier) PreviewSMSTemplate(ctx context.Context, req SMSPreviewRequest) (SMSPreview, error) {
	s, e := smsScene(req.Scene)
	if e != nil {
		return SMSPreview{}, e
	}
	v := smsExamples(s)
	v["site_name"] = n.SiteName(ctx)
	return renderSMSTemplate(req.Template, s, v)
}

const smsTemplateColumns = `id,name,provider,kind,template_code,content,parameters,enabled,range_type,remote_template_id,audit_status,audit_message,audit_updated_at,remark,sign_name,template_type,remote_operation`

func scanSMSTemplate(row interface{ Scan(...any) error }) (SMSTemplate, error) {
	var t SMSTemplate
	var raw []byte
	e := row.Scan(&t.ID, &t.Name, &t.Provider, &t.Kind, &t.TemplateCode, &t.Content, &raw, &t.Enabled, &t.RangeType, &t.RemoteTemplateID, &t.AuditStatus, &t.AuditMessage, &t.AuditUpdatedAt, &t.Remark, &t.SignName, &t.TemplateType, &t.RemoteOperation)
	if e != nil {
		return t, e
	}
	if json.Unmarshal(raw, &t.Parameters) != nil {
		return t, errors.New("短信模板参数无效")
	}
	return t, nil
}

// sameSMSParameters 比较参数映射，nil 与空映射视为相同。
func sameSMSParameters(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
func (n *Notifier) ListSMSTemplates(ctx context.Context) ([]SMSTemplate, error) {
	rows, e := n.db.QueryContext(ctx, `SELECT `+smsTemplateColumns+` FROM sms_templates ORDER BY id DESC`)
	if e != nil {
		return nil, errors.New("读取短信模板失败")
	}
	defer rows.Close()
	out := []SMSTemplate{}
	for rows.Next() {
		t, e := scanSMSTemplate(rows)
		if e != nil {
			return nil, errors.New("读取短信模板失败")
		}
		out = append(out, t)
	}
	if rows.Err() != nil {
		return nil, errors.New("读取短信模板失败")
	}
	return out, nil
}

// ponytail: 低频后台配置使用表锁串行验证依赖；高频模板管理再改为有序行锁。
func (n *Notifier) smsConfigTx(ctx context.Context) (*sql.Tx, error) {
	tx, e := n.db.BeginTx(ctx, nil)
	if e != nil {
		return nil, errors.New("短信配置事务开启失败")
	}
	if _, e = tx.ExecContext(ctx, `LOCK TABLE sms_templates,sms_scene_bindings IN SHARE ROW EXCLUSIVE MODE`); e != nil {
		tx.Rollback()
		return nil, errors.New("短信配置暂不可用")
	}
	return tx, nil
}
func smsTemplateBusy(ctx context.Context, tx *sql.Tx, id int64) error {
	var op string
	if tx.QueryRowContext(ctx, `SELECT remote_operation FROM sms_templates WHERE id=$1`, id).Scan(&op) != nil {
		return errors.New("短信模板不存在")
	}
	if op != "" {
		return errors.New("远程操作进行中或结果未确认，请先在供应商控制台核对，禁止覆盖或重复提交")
	}
	return nil
}
func (n *Notifier) SaveSMSTemplate(ctx context.Context, t SMSTemplate) (int64, error) {
	if t.ID < 0 {
		return 0, errors.New("短信模板编号无效")
	}
	if t.RangeType == "" {
		t.RangeType = SMSRangeCN
	}
	if t.AuditStatus != "" || t.AuditMessage != "" || t.AuditUpdatedAt != nil || t.RemoteTemplateID != "" || t.TemplateType != "" || t.RemoteOperation != "" {
		return 0, errors.New("远程编号、审核与远程操作字段只读")
	}
	if e := validateSMSTemplate(t); e != nil {
		return 0, e
	}
	tx, e := n.smsConfigTx(ctx)
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	// 本地内容变更会使原审核结论失效，避免沿用旧的“审核通过”状态继续发送。
	var nextAudit SMSAuditStatus
	var nextAuditMessage string
	auditChanged := false
	if t.ID > 0 {
		if e = smsTemplateBusy(ctx, tx, t.ID); e != nil {
			return 0, e
		}
		old, e := scanSMSTemplate(tx.QueryRowContext(ctx, `SELECT `+smsTemplateColumns+` FROM sms_templates WHERE id=$1`, t.ID))
		if e != nil {
			return 0, errors.New("短信模板不存在")
		}
		if old.Provider != t.Provider || old.Kind != t.Kind || old.RangeType != t.RangeType {
			return 0, errors.New("已有模板不能更改服务商、类型或范围，请新建模板")
		}
		if old.RemoteTemplateID != "" && old.TemplateCode != t.TemplateCode {
			return 0, errors.New("已关联的远程模板编码不可本地覆盖")
		}
		if old.Content != t.Content || old.SignName != t.SignName || !sameSMSParameters(old.Parameters, t.Parameters) {
			if old.AuditStatus == SMSAuditApproved || old.AuditStatus == SMSAuditPending {
				nextAudit, nextAuditMessage, auditChanged = SMSAuditUnknown, "本地正文、签名或参数映射已修改，原审核结论已失效，请重新远程提交或同步审核", true
			}
		}
		rows, e := tx.QueryContext(ctx, `SELECT code FROM sms_scene_bindings WHERE template_id=$1`, t.ID)
		if e != nil {
			return 0, errors.New("读取短信绑定失败")
		}
		var codes []string
		for rows.Next() {
			var code string
			if rows.Scan(&code) != nil {
				rows.Close()
				return 0, errors.New("读取短信绑定失败")
			}
			codes = append(codes, code)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return 0, errors.New("读取短信绑定失败")
		}
		for _, code := range codes {
			scene, e := smsScene(code)
			if e != nil {
				return 0, e
			}
			if !t.Enabled {
				return 0, errors.New("已绑定模板不可停用，请先解除绑定")
			}
			if _, e = renderSMSTemplate(t, scene, smsExamples(scene)); e != nil {
				return 0, e
			}
		}
	}
	if t.Parameters == nil {
		t.Parameters = map[string]string{}
	}
	raw, _ := json.Marshal(t.Parameters)
	if t.ID == 0 {
		audit := SMSAuditUnknown
		d, _ := SMSProviderDescriptorFor(t.Provider)
		if !d.Capabilities.TemplateCRUD {
			audit = SMSAuditNotSupported
		}
		e = tx.QueryRowContext(ctx, `INSERT INTO sms_templates(name,provider,kind,template_code,content,parameters,enabled,range_type,remark,sign_name,audit_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING id`, t.Name, t.Provider, t.Kind, t.TemplateCode, t.Content, string(raw), t.Enabled, t.RangeType, t.Remark, t.SignName, audit).Scan(&t.ID)
	} else {
		_, e = tx.ExecContext(ctx, `UPDATE sms_templates SET name=$2,template_code=$3,content=$4,parameters=$5,enabled=$6,remark=$7,sign_name=$8,
			audit_status=CASE WHEN $9 THEN $10 ELSE audit_status END,
			audit_message=CASE WHEN $9 THEN $11 ELSE audit_message END,
			audit_updated_at=CASE WHEN $9 THEN now() ELSE audit_updated_at END WHERE id=$1`,
			t.ID, t.Name, t.TemplateCode, t.Content, string(raw), t.Enabled, t.Remark, t.SignName, auditChanged, string(nextAudit), nextAuditMessage)
	}
	if e != nil {
		return 0, errors.New("保存短信模板失败，原配置未更改")
	}
	if tx.Commit() != nil {
		return 0, errors.New("短信模板提交失败")
	}
	return t.ID, nil
}
func (n *Notifier) DeleteSMSTemplate(ctx context.Context, id int64) error {
	tx, e := n.smsConfigTx(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = smsTemplateBusy(ctx, tx, id); e != nil {
		return e
	}
	var bound bool
	if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sms_scene_bindings WHERE template_id=$1)`, id).Scan(&bound) != nil {
		return errors.New("读取短信绑定失败")
	}
	if bound {
		return errors.New("模板仍被场景引用，请先解除绑定")
	}
	if _, e = tx.ExecContext(ctx, `DELETE FROM sms_templates WHERE id=$1`, id); e != nil {
		return errors.New("删除短信模板失败")
	}
	if tx.Commit() != nil {
		return errors.New("删除短信模板提交失败")
	}
	return nil
}
func (n *Notifier) ListSMSScenes(ctx context.Context) ([]SMSScene, error) {
	out := defaultSMSScenes()
	rows, e := n.db.QueryContext(ctx, `SELECT code,coalesce(template_id,0),enabled FROM sms_scene_bindings`)
	if e != nil {
		return nil, errors.New("读取短信场景失败")
	}
	defer rows.Close()
	for rows.Next() {
		var b SMSBinding
		if rows.Scan(&b.Code, &b.TemplateID, &b.Enabled) != nil {
			return nil, errors.New("读取短信场景失败")
		}
		for i := range out {
			if out[i].Code == b.Code {
				out[i].TemplateID = b.TemplateID
				out[i].Enabled = b.Enabled
			}
		}
	}
	if rows.Err() != nil {
		return nil, errors.New("读取短信场景失败")
	}
	return out, nil
}
func (n *Notifier) SaveSMSBinding(ctx context.Context, b SMSBinding) error {
	scene, e := smsScene(b.Code)
	if e != nil {
		return e
	}
	if b.TemplateID < 0 || (!scene.Required && b.Enabled && b.TemplateID == 0) {
		return errors.New("启用通知场景必须选择模板")
	}
	if scene.Required && !b.Enabled {
		return errors.New("验证码场景不可关闭")
	}
	tx, e := n.smsConfigTx(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if b.TemplateID > 0 {
		if e = smsTemplateBusy(ctx, tx, b.TemplateID); e != nil {
			return e
		}
		t, e := scanSMSTemplate(tx.QueryRowContext(ctx, `SELECT `+smsTemplateColumns+` FROM sms_templates WHERE id=$1`, b.TemplateID))
		if e != nil {
			return errors.New("短信模板不存在")
		}
		if scene.Kind == "otp" && t.RangeType != SMSRangeCN {
			return errors.New("验证码模板只能绑定国内短信通道")
		}
		route, err := n.loadSMSRoute(ctx, tx, t.RangeType)
		if err != nil {
			return err
		}
		if t.Provider != route.Provider {
			return errors.New("模板与该范围当前短信服务商不匹配")
		}
		if b.Enabled && !t.Enabled {
			return errors.New("不能绑定停用模板")
		}
		if _, e = renderSMSTemplate(t, scene, smsExamples(scene)); e != nil {
			return e
		}
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO sms_scene_bindings(code,template_id,enabled) VALUES($1,NULLIF($2,0),$3) ON CONFLICT(code) DO UPDATE SET template_id=excluded.template_id,enabled=excluded.enabled`, b.Code, b.TemplateID, b.Enabled); e != nil {
		return errors.New("保存短信场景失败")
	}
	if tx.Commit() != nil {
		return errors.New("短信场景提交失败")
	}
	return nil
}
func smsTemplateSendable(t SMSTemplate) error {
	if t.AuditStatus == SMSAuditPending || t.AuditStatus == SMSAuditRejected {
		return errors.New("短信模板审核中或审核未通过，禁止发送")
	}
	if (t.Provider == "aliyun_sms" || t.Provider == "qcloudsms" || t.Provider == "idcsmart" || t.Provider == "idcsmartpro") && t.TemplateCode == "" {
		return errors.New("短信模板尚未配置远程编码")
	}
	return nil
}
func loadSMSBinding(ctx context.Context, db emailTemplateReader, code string) (SMSTemplate, bool, error) {
	var id int64
	var enabled bool
	e := db.QueryRowContext(ctx, `SELECT coalesce(template_id,0),enabled FROM sms_scene_bindings WHERE code=$1`, code).Scan(&id, &enabled)
	if errors.Is(e, sql.ErrNoRows) {
		return SMSTemplate{}, false, nil
	}
	if e != nil {
		return SMSTemplate{}, false, errors.New("读取短信绑定失败")
	}
	if id == 0 {
		return SMSTemplate{}, false, nil
	}
	t, e := scanSMSTemplate(db.QueryRowContext(ctx, `SELECT `+smsTemplateColumns+` FROM sms_templates WHERE id=$1`, id))
	if e != nil {
		return t, true, errors.New("读取短信模板失败")
	}
	if !enabled || !t.Enabled {
		return t, true, errors.New("短信场景或模板已停用")
	}
	var operation string
	if db.QueryRowContext(ctx, `SELECT remote_operation FROM sms_templates WHERE id=$1`, id).Scan(&operation) != nil || operation != "" {
		return t, true, errors.New("短信模板远程操作未确认，禁止发送")
	}
	return t, true, smsTemplateSendable(t)
}
func (n *Notifier) SaveSMSSettings(ctx context.Context, values map[string]string) error {
	routesRaw, hasRoutes := values["sms_routes"]
	if hasRoutes {
		routes, err := parseSMSRoutes(routesRaw)
		if err != nil {
			return err
		}
		tx, err := n.smsConfigTx(ctx)
		if err != nil {
			return errors.New("短信设置事务开启失败")
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(ctx, `LOCK TABLE settings IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return errors.New("短信设置暂不可用")
		}
		if err = n.saveSMSRoutes(ctx, tx, routes); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return errors.New("短信设置提交失败")
		}
		return nil
	}
	provider := strings.ToLower(strings.TrimSpace(values["sms_provider"]))
	if _, ok := SMSProviderDescriptorFor(provider); provider != "" && !ok {
		return errors.New("短信服务商不受支持")
	}
	for key, value := range values {
		if !smsTextValid(value, 1000) {
			return errors.New("短信设置含控制字符或过长")
		}
		found := false
		for _, allowed := range smsSettingKeys {
			if key == allowed {
				found = true
			}
		}
		if !found {
			return errors.New("短信设置包含未知字段")
		}
	}
	tx, e := n.smsConfigTx(ctx)
	if e != nil {
		return errors.New("短信设置事务开启失败")
	}
	defer tx.Rollback()
	// ponytail: 设置低频写入，表锁保证单组凭据原子切换；高频配置再改专用配置行。
	if _, e = tx.ExecContext(ctx, `LOCK TABLE settings IN SHARE ROW EXCLUSIVE MODE`); e != nil {
		return errors.New("短信设置暂不可用")
	}
	var busy bool
	if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sms_templates WHERE remote_operation<>'')`).Scan(&busy) != nil || busy {
		return errors.New("存在进行中或未确认的远程短信操作，请先核对后再切换凭据")
	}
	var previous string
	if tx.QueryRowContext(ctx, `SELECT coalesce((SELECT value FROM settings WHERE key='sms_provider'),'')`).Scan(&previous) != nil {
		return errors.New("读取短信服务商失败")
	}
	switching := provider != strings.ToLower(strings.TrimSpace(previous))
	if switching && provider != "" && strings.TrimSpace(values["sms_secret_key"]) == "" && !(provider == "submail" && strings.TrimSpace(values["sms_global_secret_key"]) != "") {
		return errors.New("切换短信服务商必须重新填写密钥")
	}
	set := func(k, v string) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
		return e
	}
	if switching {
		for _, k := range smsSettingKeys {
			if set(k, "") != nil {
				return errors.New("清除旧短信凭据失败，原配置未更改")
			}
		}
	}
	for _, k := range smsSettingKeys {
		if k == "sms_api_key" || k == "sms_token" || k == "sms_user" {
			continue
		}
		v := strings.TrimSpace(values[k])
		if (k == "sms_secret_key" || k == "sms_global_secret_key") && v == "" {
			continue
		}
		if set(k, v) != nil {
			return errors.New("保存短信设置失败，原配置未更改")
		}
	}
	if secret := strings.TrimSpace(values["sms_secret_key"]); secret != "" && provider == "stay33" {
		if set("sms_api_key", secret) != nil || set("sms_token", secret) != nil {
			return errors.New("保存短信密钥失败")
		}
	}
	if set("sms_provider", provider) != nil {
		return errors.New("保存短信服务商失败")
	}
	if tx.Commit() != nil {
		return errors.New("短信设置提交失败")
	}
	return nil
}
func (n *Notifier) SendPurpose(ctx context.Context, phone, code, purpose string) error {
	scene, e := smsScene("otp_" + purpose)
	if e != nil {
		return e
	}
	t, bound, e := loadSMSBinding(ctx, n.db, scene.Code)
	if e != nil {
		return e
	}
	p := NewConfiguredSMSProvider(n.Settings)
	if !bound {
		// 未绑定时仍兼容旧验证码配置；若已迁移到路由配置，则使用国内路由而非旧全局通道。
		route, routeErr := n.loadSMSRoute(ctx, n.db, SMSRangeCN)
		if routeErr != nil || route.Provider == "" {
			return p.SendPurpose(ctx, phone, code, purpose)
		}
		return (&ConfiguredSMSProvider{}).sendPurposeWithSettings(ctx, phone, code, purpose, route.Config)
	}
	if t.RangeType != SMSRangeCN {
		return errors.New("验证码模板只能使用国内短信通道")
	}
	route, e := n.loadSMSRoute(ctx, n.db, SMSRangeCN)
	if e != nil {
		return e
	}
	if t.Provider != route.Provider {
		return errors.New("验证码模板与国内短信通道不匹配")
	}
	v := map[string]string{"code": code, "purpose": purpose, "site_name": n.SiteName(ctx), "sign_name": strings.Trim(route.Config["sms_sign_name"], "【】")}
	preview, e := renderSMSTemplate(t, scene, v)
	if e != nil {
		return e
	}
	return (&ConfiguredSMSProvider{}).sendRendered(ctx, phone, preview, route.Config, true)
}

func smsCloudContent(t SMSTemplate) (string, error) {
	used := map[string]bool{}
	var err error
	content := smsPlaceholder.ReplaceAllStringFunc(t.Content, func(token string) string {
		m := smsPlaceholder.FindStringSubmatch(token)
		key := ""
		for _, s := range m[1:] {
			if s != "" {
				key = s
			}
		}
		param := key
		if _, ok := t.Parameters[param]; !ok {
			if t.Provider == "qcloudsms" {
				if _, ok := t.Parameters["param"+key]; ok {
					param = "param" + key
				}
			}
			if _, ok := t.Parameters[param]; !ok {
				found := ""
				for p, v := range t.Parameters {
					if v == key {
						if found != "" {
							err = errors.New("模板变量映射有歧义，请使用供应商参数名")
						}
						found = p
					}
				}
				param = found
			}
		}
		if param == "" {
			err = errors.New("模板正文变量没有对应参数映射")
		}
		used[param] = true
		if t.Provider == "qcloudsms" {
			return "{" + strings.TrimPrefix(param, "param") + "}"
		}
		return "${" + param + "}"
	})
	if strings.ContainsAny(smsPlaceholder.ReplaceAllString(t.Content, ""), "{}") {
		err = errors.New("模板变量格式错误")
	}
	for param := range t.Parameters {
		if !used[param] {
			err = errors.New("模板参数映射未在正文中使用")
		}
	}
	return content, err
}
func smsRemoteTemplate(t SMSTemplate) SMSProviderTemplate {
	content := t.Content
	if t.Provider == "aliyun_sms" || t.Provider == "qcloudsms" {
		content, _ = smsCloudContent(t)
	}
	if t.Provider == "submail" || t.Provider == "idcsmart" || t.Provider == "idcsmartpro" {
		content = smsPlaceholder.ReplaceAllStringFunc(content, func(token string) string {
			m := smsPlaceholder.FindStringSubmatch(token)
			for _, key := range m[1:] {
				if key != "" {
					return "@var(" + key + ")"
				}
			}
			return token
		})
	}
	return SMSProviderTemplate{ID: t.TemplateCode, Name: t.Name, Content: content, SignName: t.SignName, Kind: t.Kind, Range: t.RangeType, Remark: t.Remark, Parameters: t.Parameters}
}
func (n *Notifier) RemoteSMSTemplate(ctx context.Context, id int64, action string) error {
	if id <= 0 || (action != "create" && action != "update" && action != "query" && action != "delete") {
		return errors.New("远程模板操作无效")
	}
	// 提交持久标记后才调用供应商；进程中断保留标记，绝不自动重试收费或创建操作。
	tx, e := n.smsConfigTx(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = smsTemplateBusy(ctx, tx, id); e != nil {
		return e
	}
	t, e := scanSMSTemplate(tx.QueryRowContext(ctx, `SELECT `+smsTemplateColumns+` FROM sms_templates WHERE id=$1`, id))
	if e != nil {
		return errors.New("短信模板不存在")
	}
	route, e := n.loadSMSRoute(ctx, tx, t.RangeType)
	if e != nil {
		return e
	}
	if route.Provider != t.Provider {
		return errors.New("模板与该范围当前短信服务商不匹配")
	}
	p, e := NewSMSProvider(t.Provider, route.Config, nil)
	if e != nil {
		return e
	}
	if !p.Descriptor().Capabilities.TemplateCRUD || (t.Provider == "submail" && t.RangeType == SMSRangeGlobal) {
		return errors.New("该服务商不支持此范围的远程模板操作")
	}
	if action == "create" && t.TemplateCode != "" {
		return errors.New("模板已有远程编码，禁止重复创建")
	}
	if action != "create" && t.TemplateCode == "" {
		return errors.New("模板尚无远程编码")
	}
	if action == "delete" {
		var bound bool
		if tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sms_scene_bindings WHERE template_id=$1)`, id).Scan(&bound) != nil {
			return errors.New("读取短信绑定失败")
		}
		if bound {
			return errors.New("绑定模板禁止远程删除，请先解绑")
		}
	}
	if (action == "create" || action == "update") && strings.TrimSpace(t.Content) == "" {
		return errors.New("提交远程模板必须填写正文")
	}
	if _, e = tx.ExecContext(ctx, `UPDATE sms_templates SET remote_operation=$2 WHERE id=$1`, id, action); e != nil {
		return errors.New("保存远程操作标记失败，未提交供应商")
	}
	if tx.Commit() != nil {
		return errors.New("保存远程操作标记失败，未提交供应商")
	}
	remote := smsRemoteTemplate(t)
	var result SMSProviderResult
	switch action {
	case "create":
		result, e = p.TemplateCreate(ctx, remote)
	case "update":
		result, e = p.TemplateUpdate(ctx, remote)
	case "query":
		result, e = p.TemplateQuery(ctx, t.TemplateCode, t.RangeType)
	case "delete":
		result, e = p.TemplateDelete(ctx, t.TemplateCode, t.RangeType)
	}
	finish, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e != nil {
		var unknown smsUnknownError
		if errors.As(e, &unknown) && action != "query" {
			_, _ = n.db.ExecContext(finish, `UPDATE sms_templates SET audit_status='pending',audit_message='远程操作结果未知，禁止自动重试；请联系管理员核对供应商控制台' WHERE id=$1`, id)
			return errors.New("远程操作结果未知，已锁定模板防止重复提交，请核对供应商控制台")
		}
		if _, err := n.db.ExecContext(finish, `UPDATE sms_templates SET remote_operation='' WHERE id=$1`, id); err != nil {
			return errors.New("远程操作失败且解锁失败，请核对供应商控制台")
		}
		return e
	}
	if action == "query" && result.Status == SMSAuditUnknown && (t.AuditStatus == SMSAuditPending || t.AuditStatus == SMSAuditRejected) {
		result.Status = t.AuditStatus
	}
	code := t.TemplateCode
	if action == "create" {
		code = result.ProviderTemplateID
	}
	if action == "delete" {
		code = ""
	}
	_, e = n.db.ExecContext(finish, `UPDATE sms_templates SET template_code=$2,remote_template_id=$2,audit_status=$3,audit_message=$4,audit_updated_at=now(),remote_operation='' WHERE id=$1`, id, code, result.Status, result.Message)
	if e != nil {
		return errors.New("供应商已处理但本地写回失败，模板保持锁定；远程模板编号：" + code + "；请核对供应商控制台，勿重复提交")
	}
	return nil
}

// ResolveSMSTemplateLock 由管理员在核对供应商控制台后手动解锁。
// 只清除锁定标记并把审核状态置为待同步，绝不自动重放创建、修改、删除或发送。
func (n *Notifier) ResolveSMSTemplateLock(ctx context.Context, id int64) error {
	if id <= 0 {
		return errors.New("短信模板编号无效")
	}
	tx, e := n.smsConfigTx(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var operation string
	if tx.QueryRowContext(ctx, `SELECT remote_operation FROM sms_templates WHERE id=$1`, id).Scan(&operation) != nil {
		return errors.New("短信模板不存在")
	}
	if operation == "" {
		return errors.New("该模板当前没有未确认的远程操作，无需解锁")
	}
	if _, e = tx.ExecContext(ctx, `UPDATE sms_templates SET remote_operation='',audit_status='unknown',
		audit_message='管理员已确认核对供应商控制台并手动解锁，请重新执行查询审核同步最新状态',audit_updated_at=now() WHERE id=$1`, id); e != nil {
		return errors.New("解锁失败，原状态未更改")
	}
	if tx.Commit() != nil {
		return errors.New("解锁提交失败")
	}
	return nil
}
