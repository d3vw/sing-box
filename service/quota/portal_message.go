package quota

import (
	"html/template"
	"strings"
)

// messagePage is the plain page the portal answers with outside the
// dashboard: errors, and results of form posts sent without the page script.
// It follows the dashboard's theme and language choice.
var messagePageTemplate = template.Must(template.New("message").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Title}}</title>
<script>try{var t=localStorage.getItem("quota-theme");if(t!=="light"&&t!=="dark")t=matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light";document.documentElement.dataset.theme=t}catch(e){}</script>
<style>
:root{--page:#e3e5e1;--panel:#fafaf8;--panel-border:#d2d5cf;--ink:#16191c;--ink2:#3e4640;--mute:#575f59;--accent-text:oklch(0.45 0.16 262);--shadow:0 24px 48px -32px rgba(30,40,35,.35);--hl:#fff}
[data-theme="dark"]{--page:#0f1112;--panel:#17191b;--panel-border:#2a2e30;--ink:#e8eae6;--ink2:#b4bbb5;--mute:#8e968f;--accent-text:oklch(0.8 0.11 262);--shadow:0 24px 48px -28px rgba(0,0,0,.8);--hl:rgba(255,255,255,.04);color-scheme:dark}
*{box-sizing:border-box;margin:0}
html,body{background:var(--page)}
body{display:flex;min-height:100dvh;align-items:center;justify-content:center;padding:20px;color:var(--ink);font:14px/1.6 "Noto Sans SC","PingFang SC","Hiragino Sans GB","Microsoft YaHei",system-ui,sans-serif;-webkit-font-smoothing:antialiased}
main{display:flex;width:100%;max-width:440px;flex-direction:column;gap:10px;border-radius:20px;border:1px solid var(--panel-border);background:var(--panel);padding:28px;box-shadow:inset 0 1px 0 var(--hl),var(--shadow)}
h1{font-size:17px;font-weight:600}
p{color:var(--ink2);overflow-wrap:anywhere}
p.note{font-size:13px;color:var(--mute)}
a{margin-top:6px;align-self:flex-start;font-weight:600;color:var(--accent-text);text-decoration:none;text-underline-offset:3px}
a:hover{text-decoration:underline}
</style>
</head>
<body>
<main>
<h1 data-i18n="{{.Message}}">{{.Message}}</h1>
{{with .Detail}}<p>{{.}}</p>{{end}}
{{with .Note}}<p class="note" data-i18n="{{.}}">{{.}}</p>{{end}}
<a href="/" data-i18n="Back">Back</a>
</main>
<script>
(function(){
try{if(localStorage.getItem("quota-language")==="en")return}catch(e){}
var zh={{.Chinese}};
document.documentElement.lang="zh-CN";
Array.prototype.forEach.call(document.querySelectorAll("[data-i18n]"),function(e){var v=zh[e.getAttribute("data-i18n")];if(v)e.textContent=v});
if(zh[document.title])document.title=zh[document.title];
})();
</script>
</body>
</html>
`))

// messageChinese translates the portal's messages; English is the default
// text so the page reads without the script.
var messageChinese = map[string]string{
	"Back":                             "返回",
	"Forbidden":                        "无权访问",
	"Bad request":                      "请求无效",
	"Not found":                        "未找到",
	"User not found":                   "未找到用户",
	"Missing inbound tag or user name": "缺少入站标签或用户名",
	"Invalid Shadowsocks outbound":     "Shadowsocks 出站配置无效",
	"Failed to save":                   "保存失败",
	"Delete custom outbound failed":    "移除自定义出站失败",
	"Error":                            "出错了",
	"Saved, but the connectivity check failed":                                  "已保存，但连通性检查没通过",
	"If the server works for you, ignore this. Otherwise use Test to re-check.": "如果这台服务器能正常用，可以忽略；否则请用「测试连接」再检查一次。",
}

type messagePage struct {
	Title, Message, Detail, Note string
	Chinese                      map[string]string
}

// renderMessagePage renders message as the heading, with an optional detail
// (shown as is) and note (translated like message).
func renderMessagePage(message, detail, note string) string {
	var sb strings.Builder
	err := messagePageTemplate.Execute(&sb, messagePage{Title: message, Message: message, Detail: detail, Note: note, Chinese: messageChinese})
	if err != nil {
		return template.HTMLEscapeString(message + ": " + detail)
	}
	return sb.String()
}
