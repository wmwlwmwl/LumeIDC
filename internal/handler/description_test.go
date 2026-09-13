package handler

import (
	"strings"
	"testing"
)

// 上游描述常内嵌 <style>（魔方财务商品详情就是「<div><style>…</style><div class="config-row">…」）。
// 只去标签会把 CSS 源码当正文，列表卡片/购买页就会显示一堆 `.config-row {display: flex…}`。
func TestSafeDescriptionHTMLDropsStyleContent(t *testing.T) {
	in := `<div style="display:flex">` +
		`<style>.config-row {display: flex; gap: 5px;} .config-value {font-weight: 600;}</style>` +
		`<div class="config-row">CPU16核</div></div>`
	got := string(safeDescriptionHTML(in))
	if strings.Contains(got, "config-row {") || strings.Contains(got, "font-weight") {
		t.Fatalf("CSS 文本不应进入描述: %s", got)
	}
	if !strings.Contains(got, "CPU16核") {
		t.Fatalf("正文应保留: %s", got)
	}
}

func TestSafeDescriptionHTMLDropsScriptContent(t *testing.T) {
	got := string(safeDescriptionHTML(`<p>测试</p><script>alert(1)</script>`))
	if strings.Contains(got, "alert") {
		t.Fatalf("脚本内容不应进入描述: %s", got)
	}
	if !strings.Contains(got, "测试") {
		t.Fatalf("正文应保留: %s", got)
	}
}

// 上游另一种常见格式：整段 HTML 被实体转义（&lt;ul class=&#34;spec&#34;&gt;），
// 需要先还原实体，且白名单里的 ul/li/span 与其 class 要保留（class 供主题样式挂钩）。
func TestSafeDescriptionHTMLUnescapesAndKeepsList(t *testing.T) {
	in := "&lt;ul class=&#34;spec&#34;&gt;\r\n" +
		"&lt;li class=&#34;s-cpu&#34;&gt;处理器型号：Platinum 8259CL&lt;/li&gt;\r\n" +
		"&lt;li class=&#34;s-core&#34;&gt;核　　　心：16核&lt;/li&gt;\r\n" +
		"&lt;/ul&gt;\r\n" +
		"&lt;span class=&#34;tag s-user&#34;&gt;人工过白&lt;/span&gt;"
	got := string(safeDescriptionHTML(in))
	t.Logf("实际渲染: %s", got)
	for _, want := range []string{
		`<ul class="spec">`,
		`<li class="s-cpu">处理器型号：Platinum 8259CL</li>`,
		`</ul>`,
		`<span class="tag s-user">人工过白</span>`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %s\n实际: %s", want, got)
		}
	}
	if strings.Contains(got, "&lt;") || strings.Contains(got, "&#34;") {
		t.Fatalf("实体应已还原: %s", got)
	}
}

// 上游「一行一个 div」的描述（如 <div class="config-row">布局）去掉标签后必须有断行，
// 否则规格会全粘成一坨（卡片上就是 "CPU16核 intel E5内存32GB…"）。
func TestSafeDescriptionHTMLBreaksDroppedBlocks(t *testing.T) {
	in := `<div class="config-row"><div class="k">CPU</div><div class="v">16核 intel E5</div></div>` +
		`<div class="config-row"><div class="k">内存</div><div class="v">32GB</div></div>`
	got := string(safeDescriptionHTML(in))
	t.Logf("实际渲染: %s", got)
	if strings.Contains(got, "div") {
		t.Fatalf("div 壳应被去掉: %s", got)
	}
	if want := "CPU<br>16核 intel E5<br>内存<br>32GB"; got != want {
		t.Fatalf("每项应各占一行且不留首尾空行\n期望: %s\n实际: %s", want, got)
	}
}

// 上游「外层 flex-column 堆行、行内 flex 放图标+标签+值」的描述（348 那种）：
// 行内多列要用空格拼成一行，只有行与行之间才换行 → 「CPU 16核 intel E5」。
func TestSafeDescriptionHTMLKeepsRowInline(t *testing.T) {
	in := `<div style="display: flex; flex-direction: column; gap: 10px;">` +
		`<style>.config-row {display: flex; align-items: center; gap: 5px;}</style>` +
		`<div class="config-row">` +
		`<div class="config-icon" style="background: #f3e8ff;"><svg width="16"><rect x="4"/></svg></div>` +
		`<div class="config-label" style="color: #a78bfa;">CPU</div>` +
		`<div class="config-value">16核 intel E5</div></div>` +
		`<div class="config-row">` +
		`<div class="config-label">内存</div><div class="config-value">32GB</div></div>` +
		`</div>`
	got := string(safeDescriptionHTML(in))
	t.Logf("实际渲染: %s", got)
	if want := "CPU 16核 intel E5<br>内存 32GB"; got != want {
		t.Fatalf("行内应同行、行间应换行\n期望: %s\n实际: %s", want, got)
	}
}

// 上游描述里的外链：只放行 http/https，并统一加固（新窗口 + noopener/nofollow）。
func TestSafeDescriptionHTMLLinks(t *testing.T) {
	in := `<a href="https://nodequality.com/r/abc" target="_blank">` +
		`<strong style="color:red;font-size:14px;">线路测试报告 </strong></a>` +
		`<br><a href="http://103.231.59.166/1.zip" target="_blank">下载测速文件</a>`
	got := string(safeDescriptionHTML(in))
	t.Logf("实际渲染: %s", got)
	want := `<a href="https://nodequality.com/r/abc" target="_blank" rel="noopener noreferrer nofollow"><strong>线路测试报告</strong></a>` +
		`<br><a href="http://103.231.59.166/1.zip" target="_blank" rel="noopener noreferrer nofollow">下载测速文件</a>`
	if got != want {
		t.Fatalf("链接应保留并加固\n期望: %s\n实际: %s", want, got)
	}
}

// 可执行 scheme / 无 scheme 的链接整对丢掉（文字保留，不留孤立 </a>）。
func TestSafeDescriptionHTMLDropsUnsafeLinks(t *testing.T) {
	for _, in := range []string{
		`<a href="javascript:alert(1)">点我</a>`,
		`<a href="data:text/html;base64,PA==">点我</a>`,
		`<a href="/user/verification">点我</a>`,
		`<a>没有 href</a>`,
	} {
		got := string(safeDescriptionHTML(in))
		if strings.Contains(got, "<a") || strings.Contains(got, "</a>") {
			t.Fatalf("不安全链接不应保留标签: %s → %s", in, got)
		}
		if !strings.Contains(got, "点") && !strings.Contains(got, "没有") {
			t.Fatalf("文字应保留: %s → %s", in, got)
		}
	}
}

// 排版标签白名单外的标签只去壳留文；允许的标签保留且不带属性（防脚本入口）。
func TestSafeDescriptionHTMLKeepsInlineTags(t *testing.T) {
	got := string(safeDescriptionHTML(`<b onclick="hack()">加粗</b><iframe src="x"></iframe>正常`))
	if !strings.Contains(got, "<b>加粗</b>") {
		t.Fatalf("应保留无属性的 <b>: %s", got)
	}
	if strings.Contains(got, "onclick") || strings.Contains(got, "iframe") {
		t.Fatalf("属性/危险标签应被移除: %s", got)
	}
}
