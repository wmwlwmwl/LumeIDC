package refund

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// channelTimeout 单次 webhook 推送超时。
const channelTimeout = 5 * time.Second

// notifyChannels 向已开启的机器人渠道推送文本通知（异步，不阻塞主流程）。
func (p *Plugin) notifyChannels(ctx context.Context, title, content string) {
	channels := p.loadChannels(ctx)
	if len(channels) == 0 {
		return
	}
	text := title + "\n" + content
	for _, ch := range channels {
		go p.pushChannel(ch.url, ch.kind, text)
	}
}

type botChannel struct {
	kind string // wecom|dingtalk|feishu
	url  string
}

func (p *Plugin) loadChannels(ctx context.Context) []botChannel {
	var out []botChannel
	if p.cfgBool(ctx, "notifyWecom", false) {
		if u := p.cfg(ctx, "notifyWecomUrl", ""); u != "" {
			out = append(out, botChannel{kind: "wecom", url: u})
		}
	}
	if p.cfgBool(ctx, "notifyDingtalk", false) {
		if u := p.cfg(ctx, "notifyDingtalkUrl", ""); u != "" {
			out = append(out, botChannel{kind: "dingtalk", url: u})
		}
	}
	if p.cfgBool(ctx, "notifyFeishu", false) {
		if u := p.cfg(ctx, "notifyFeishuUrl", ""); u != "" {
			out = append(out, botChannel{kind: "feishu", url: u})
		}
	}
	return out
}

func (p *Plugin) pushChannel(rawURL, kind, text string) {
	var body []byte
	switch kind {
	case "wecom", "dingtalk":
		body, _ = json.Marshal(map[string]any{
			"msgtype": "text",
			"text":    map[string]string{"content": text},
		})
	case "feishu":
		body, _ = json.Marshal(map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": text},
		})
	default:
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), channelTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(body))
	if err != nil {
		log.Printf("[refund] %s webhook 构造失败: %v", kind, err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("[refund] %s webhook 推送失败: %v", kind, err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Printf("[refund] %s webhook 响应异常: HTTP %d", kind, resp.StatusCode)
	}
}
