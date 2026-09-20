package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"lumeidc/internal/middleware"
	"lumeidc/internal/service"
)

const smsTemplateRequestLimit = 32 << 10

func decodeSMSTemplateJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		emailTemplateError(w, http.StatusUnsupportedMediaType, "请使用 JSON 格式提交")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, smsTemplateRequestLimit)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			emailTemplateError(w, http.StatusRequestEntityTooLarge, "短信模板请求过大，请缩短内容或减少参数")
		} else {
			emailTemplateError(w, http.StatusBadRequest, "读取请求失败，请重试")
		}
		return false
	}
	raw = bytes.TrimSpace(raw)
	if !utf8.Valid(raw) || len(raw) == 0 || raw[0] != '{' {
		emailTemplateError(w, http.StatusBadRequest, "请使用有效的 UTF-8 JSON 对象")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		emailTemplateError(w, http.StatusBadRequest, "请求格式或字段无效，请检查后重试")
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		emailTemplateError(w, http.StatusBadRequest, "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}

func (a *Admin) requireSMSTemplates(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if !adminRequire(w, r) {
		return false
	}
	if a.Notifier == nil {
		emailTemplateError(w, http.StatusServiceUnavailable, "短信服务暂不可用，请稍后重试")
		return false
	}
	return true
}

func (a *Admin) adminSMSTemplates(w http.ResponseWriter, r *http.Request) {
	if !a.requireSMSTemplates(w, r) {
		return
	}
	list, err := a.Notifier.ListSMSTemplates(r.Context())
	if err != nil {
		emailTemplateError(w, http.StatusInternalServerError, "读取短信模板失败，请稍后重试")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "list": list})
}

func (a *Admin) adminSMSTemplateSave(w http.ResponseWriter, r *http.Request) {
	if !a.requireSMSTemplates(w, r) {
		return
	}
	var draft struct {
		ID           int64             `json:"id"`
		Name         string            `json:"name"`
		Provider     string            `json:"provider"`
		Kind         string            `json:"kind"`
		TemplateCode string            `json:"template_code"`
		Content      string            `json:"content"`
		Parameters   map[string]string `json:"parameters"`
		Enabled      bool              `json:"enabled"`
		RangeType    service.SMSRange  `json:"range_type"`
		Remark       string            `json:"remark"`
		SignName     string            `json:"sign_name"`
	}
	if !decodeSMSTemplateJSON(w, r, &draft) {
		return
	}
	input := service.SMSTemplate{ID: draft.ID, Name: draft.Name, Provider: draft.Provider, Kind: draft.Kind, TemplateCode: draft.TemplateCode, Content: draft.Content, Parameters: draft.Parameters, Enabled: draft.Enabled, RangeType: draft.RangeType, Remark: draft.Remark, SignName: draft.SignName}
	if input.ID < 0 {
		emailTemplateError(w, http.StatusBadRequest, "模板编号无效")
		return
	}
	id, err := a.Notifier.SaveSMSTemplate(r.Context(), input)
	if err != nil {
		emailTemplateError(w, http.StatusBadRequest, "保存失败，请检查名称、服务商、类型、正文及参数映射；模板须兼容已绑定场景，验证码模板不能停用。当前草稿已保留")
		return
	}
	a.recordSMSChange(r, "sms_template_saved", "sms_template", id, "")
	writeJSON(w, map[string]any{"ok": 1, "id": id, "msg": "短信模板已本地保存，不代表供应商审核通过"})
}

func (a *Admin) adminSMSTemplateDelete(w http.ResponseWriter, r *http.Request) {
	if !a.requireSMSTemplates(w, r) {
		return
	}
	var input struct {
		ID      int64 `json:"id"`
		Confirm bool  `json:"confirm"`
	}
	if !decodeSMSTemplateJSON(w, r, &input) {
		return
	}
	if input.ID <= 0 || !input.Confirm {
		emailTemplateError(w, http.StatusBadRequest, "请确认删除有效的短信模板")
		return
	}
	if err := a.Notifier.DeleteSMSTemplate(r.Context(), input.ID); err != nil {
		emailTemplateError(w, http.StatusBadRequest, "删除失败，正在绑定的模板不可删除，请先解绑或稍后重试")
		return
	}
	a.recordSMSChange(r, "sms_template_deleted", "sms_template", input.ID, "")
	writeJSON(w, map[string]any{"ok": 1, "msg": "短信模板已删除"})
}

func (a *Admin) adminSMSScenes(w http.ResponseWriter, r *http.Request) {
	if !a.requireSMSTemplates(w, r) {
		return
	}
	list, err := a.Notifier.ListSMSScenes(r.Context())
	if err != nil {
		emailTemplateError(w, http.StatusInternalServerError, "读取短信业务场景失败，请稍后重试")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "list": list})
}

func (a *Admin) adminSMSBindingSave(w http.ResponseWriter, r *http.Request) {
	if !a.requireSMSTemplates(w, r) {
		return
	}
	var input service.SMSBinding
	if !decodeSMSTemplateJSON(w, r, &input) {
		return
	}
	if input.TemplateID < 0 || strings.TrimSpace(input.Code) == "" || len(input.Code) > 100 {
		emailTemplateError(w, http.StatusBadRequest, "请选择有效业务场景和模板")
		return
	}
	if err := a.Notifier.SaveSMSBinding(r.Context(), input); err != nil {
		emailTemplateError(w, http.StatusBadRequest, "绑定保存失败：仅能绑定当前通道下已启用且类型、变量匹配的模板；验证码不能关闭，通知解绑须关闭。请检查通道配置后重试")
		return
	}
	a.recordSMSChange(r, "sms_binding_saved", "sms_scene", input.TemplateID, input.Code)
	writeJSON(w, map[string]any{"ok": 1, "msg": "短信业务场景绑定已保存"})
}

func (a *Admin) adminSMSTemplatePreview(w http.ResponseWriter, r *http.Request) {
	if !a.requireSMSTemplates(w, r) {
		return
	}
	var input service.SMSPreviewRequest
	if !decodeSMSTemplateJSON(w, r, &input) {
		return
	}
	preview, err := a.Notifier.PreviewSMSTemplate(r.Context(), input)
	if err != nil {
		emailTemplateError(w, http.StatusBadRequest, "预览失败，请选择同类型场景，检查模板及变量映射")
		return
	}
	writeJSON(w, map[string]any{"ok": 1, "preview": preview})
}

func (a *Admin) adminSMSDeliveries(w http.ResponseWriter, r *http.Request) {
	if !a.requireSMSTemplates(w, r) {
		return
	}
	list, err := a.Notifier.ListSMSDeliveries(r.Context())
	if err != nil {
		emailTemplateError(w, http.StatusInternalServerError, "读取短信投递记录失败，请稍后重试")
		return
	}
	if len(list) > 100 {
		list = list[:100]
	}
	writeJSON(w, map[string]any{"ok": 1, "list": list})
}

func (a *Admin) adminSMSProviders(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !adminRequire(w, r) {
		return
	}
	list := []service.ProviderDescriptor{}
	for _, d := range service.SMSProviderRegistry() {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Key < list[j].Key })
	writeJSON(w, map[string]any{"ok": 1, "list": list})
}
func (a *Admin) adminSMSTemplateRemote(w http.ResponseWriter, r *http.Request) {
	if !a.requireSMSTemplates(w, r) {
		return
	}
	var input struct {
		ID      int64  `json:"id"`
		Action  string `json:"action"`
		Confirm bool   `json:"confirm"`
	}
	if !decodeSMSTemplateJSON(w, r, &input) {
		return
	}
	if input.ID <= 0 || !input.Confirm || (input.Action != "create" && input.Action != "update" && input.Action != "query" && input.Action != "delete") {
		emailTemplateError(w, http.StatusBadRequest, "请明确确认有效的远程模板操作")
		return
	}
	a.recordSMSChange(r, "sms_template_remote_requested", "sms_template", input.ID, input.Action)
	if err := a.Notifier.RemoteSMSTemplate(r.Context(), input.ID, input.Action); err != nil {
		a.recordSMSChange(r, "sms_template_remote_failed", "sms_template", input.ID, input.Action+"："+err.Error())
		emailTemplateError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.recordSMSChange(r, "sms_template_remote_completed", "sms_template", input.ID, input.Action)
	writeJSON(w, map[string]any{"ok": 1, "msg": "远程模板操作已完成，请刷新查看审核状态"})
}

func (a *Admin) adminSMSTemplateUnlock(w http.ResponseWriter, r *http.Request) {
	if !a.requireSMSTemplates(w, r) {
		return
	}
	var input struct {
		ID      int64 `json:"id"`
		Confirm bool  `json:"confirm"`
	}
	if !decodeSMSTemplateJSON(w, r, &input) {
		return
	}
	if input.ID <= 0 || !input.Confirm {
		emailTemplateError(w, http.StatusBadRequest, "请先确认已在供应商控制台核对结果，再解除锁定")
		return
	}
	if err := a.Notifier.ResolveSMSTemplateLock(r.Context(), input.ID); err != nil {
		emailTemplateError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.recordSMSChange(r, "sms_template_unlocked", "sms_template", input.ID, "")
	writeJSON(w, map[string]any{"ok": 1, "msg": "已解除远程操作锁定，不会自动重放远程操作；请重新执行查询审核同步状态"})
}

func (a *Admin) recordSMSChange(r *http.Request, action, target string, id int64, detail string) {
	if a.AdminLog != nil {
		a.AdminLog.Record(middleware.FromSession(r.Context()).UserID, action, target, id, detail, requestIP(r))
	}
}
