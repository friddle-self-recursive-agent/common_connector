// send-email —— 通知 OpsConnector 的部署侧直调入口（探针/装配两用 CLI）。
//
// 用途：① 验收探针——经 SMTP 真实发送一封邮件（内容=loop 验收摘要），机械可验
// （exit 0 + result JSON）；② 部署侧装配基元——cored 进程内自动投递未接线期
// （引擎主体余量，cored-bridge.md §2.3），通知由装配侧经本入口出站（依赖方向不反转：
// 本命令住 connector 仓，workservice 契约仓不 import 本仓）。
//
// 配置面（全部部署侧，无一入仓）：
//
//	env QQ_SMTP_AUTH_CODE   授权码（引用式取值 secret.source=env:QQ_SMTP_AUTH_CODE）
//	--host / --port         SMTP 主机/端口（port 0→465 implicit TLS）
//	--from                  发件账号
//	--to                    收件人清单（逗号分隔；收件人可配置，缺省亦可由配置面注入）
//	--subject / --body-file 正文与主题（body-file='-' 读 stdin）
//	--probe                 只做通道可达性探测（不发送）
//
// 输出：成功=SendResult JSON（含 messageId/to）；失败=错误文本 + exit 1。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/friddle-self-recursive-agent/common_connector/notify"
)

func main() {
	host := flag.String("host", envOr("NOTIFY_SMTP_HOST", ""), "SMTP 主机（部署侧配置面）")
	port := flag.Int("port", 465, "SMTP 端口（0=缺省 465 SSL）")
	from := flag.String("from", envOr("NOTIFY_EMAIL_FROM", ""), "发件账号")
	to := flag.String("to", envOr("NOTIFY_EMAIL_TO", ""), "收件人清单（逗号分隔）")
	subject := flag.String("subject", "", "主题")
	bodyFile := flag.String("body-file", "", "正文文件（'-'=stdin）")
	probe := flag.Bool("probe", false, "只探测通道可达性（不发送）")
	timeout := flag.Duration("timeout", 30*time.Second, "超时")
	flag.Parse()

	cfg := notify.Config{Host: *host, Port: *port, From: *from,
		SecretSource: "env:QQ_SMTP_AUTH_CODE",
		To:           splitList(*to)}
	c := notify.New(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	if *probe {
		if err := c.Probe(ctx); err != nil {
			fail(err)
		}
		out, _ := json.Marshal(map[string]any{"probe": "ok", "host": *host, "port": *port})
		fmt.Println(string(out))
		return
	}

	body, err := readBody(*bodyFile)
	if err != nil {
		fail(err)
	}
	if *subject == "" {
		fail(fmt.Errorf("--subject 必填"))
	}
	params, _ := json.Marshal(notify.NotifyParams{Subject: *subject, Body: body})
	out, err := c.Invoke(ctx, notify.OpSendEmail, params)
	if err != nil {
		fail(err)
	}
	fmt.Println(string(out))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "send-email:", err)
	os.Exit(1)
}

func readBody(p string) (string, error) {
	switch p {
	case "-":
		b, err := io.ReadAll(os.Stdin)
		return string(b), err
	case "":
		b, err := io.ReadAll(os.Stdin)
		if err == nil && len(b) > 0 {
			return string(b), nil
		}
		return "", nil
	default:
		b, err := os.ReadFile(p)
		return string(b), err
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
