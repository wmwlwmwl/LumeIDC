package handler

import (
 "encoding/json"
 "log"
 "net/http"
 "strconv"

 "lumeidc/internal/middleware"
 "lumeidc/internal/service"
)

func (m *AdminManage) ServiceRecovery(w http.ResponseWriter,r *http.Request) {
 if !m.require(w,r) {return}
 id,err:=strconv.ParseInt(r.PathValue("id"),10,64);if err!=nil || id<=0 {jsonStatus(w,r,400,"服务标识无效");return}
 if r.Method==http.MethodGet {
  summary,err:=m.Payment.FulfillmentRecovery(r.Context(),id)
  if err!=nil {jsonStatus(w,r,409,"未找到可核对的隔离任务，请刷新服务列表");return}
  writeJSON(w,map[string]any{"ok":1,"recovery":summary});return
 }
 // JSON 与表单统一显式验证会话令牌；外层 CSRF 中间件仍保留。
 if !checkCSRF(r,r.Header.Get("X-CSRF-Token")) {jsonStatus(w,r,403,"页面已过期，请刷新后重试");return}
 var req service.RecoveryConfirmation
 r.Body=http.MaxBytesReader(w,r.Body,16384)
 dec:=json.NewDecoder(r.Body);dec.DisallowUnknownFields()
 if err:=dec.Decode(&req);err!=nil {jsonStatus(w,r,400,"核对参数格式无效");return}
 sess:=middleware.FromSession(r.Context())
 if err:=m.Payment.ConfirmFulfillmentRecovery(r.Context(),sess.UserID,id,r.RemoteAddr,req);err!=nil {
  log.Printf("[履约对账] 服务 %d 恢复未提交: %v",id,err)
  jsonStatus(w,r,409,"核对未通过或有并发操作，未解除隔离；请刷新证据并检查主机、账务、任务版本与支持范围")
  return
 }
 writeJSON(w,map[string]any{"ok":1})
}
