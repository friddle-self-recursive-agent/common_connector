// email.go —— 报文编码辅助（RFC 2047 主题 / base64 正文折行）与注册入口。
// 拆出纯函数便于单测（编码不改语义：解码方可还原原文）。
package notify

import (
	"encoding/base64"
	"mime"
	"strings"
)

// mimeQEncode 主题编码：纯 ASCII 原样（可读性优先）；含非 ASCII 用 RFC 2047 Q 编码。
func mimeQEncode(s string) string {
	if isASCII(s) {
		return s
	}
	return mime.QEncoding.Encode("utf-8", s)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 0x7f {
			return false
		}
	}
	return true
}

// base64Wrap 正文 base64 并按 RFC 5322 998 字符限制折行（76 列惯例）。
func base64Wrap(s string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(s))
	var b strings.Builder
	for len(enc) > 76 {
		b.WriteString(enc[:76])
		b.WriteString("\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
	return b.String()
}
