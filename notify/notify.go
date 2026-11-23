// Package notify —— 通知 OpsConnector（R-ENG-3 通用类实现，default user-system 的
// Event 默认通知通道：notify / send-email 操作）。
//
// 归属与方向：本包是 common_connector 仓的首个实现，依赖方向恒为 本仓 →
// core_system_workservice/connectors 契约层（OpsConnector 五方法：Type/Version/Ops/
// Invoke/Probe）——本仓实现契约，契约仓绝不 import 本仓。
//
// 声明面/运行时面分离（provider.md 规则 6）：SMTP 主机/端口/发件账号/收件人清单在
// Provider（kind: email）/WorkService 配置面；授权码只允许引用式取值
// secret: {source: "env:QQ_SMTP_AUTH_CODE"}——真实值只在部署侧环境变量，绝不入仓。
package notify

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"os"
	"strings"
	"time"

	"github.com/friddle-self-recursive-agent/core_system_workservice/connectors"
)

// ops 词汇（WorkService.tools 声明面同步维护）。
const (
	OpNotify    = "notify"     // 通用通知入口（本次实现=email 通道）
	OpSendEmail = "send-email" // 邮件直达 op
)

// Type notify-email：通知邮件连接器类型标注。
const Type = "notify-email"

// Version 实现版本（只加不改）。
const Version = "v0.1.0"

// Config 连接器配置（Provider/WorkService 配置面投影；真实值部署侧注入）。
type Config struct {
	Host string   // SMTP 主机（运行时实例值只存部署侧，声明面为占位符）
	Port int      // 端口；0 → 465（implicit TLS，SSL）
	From string   // 发件账号
	To   []string // 收件人清单（默认收件人；可被单次调用参数覆盖）
	// SecretSource 授权码取值来源（引用式："env:VAR"）；ResolveSecret 解析。
	// Secret 已解析授权码（部署侧装配直接注入时的通道；测试用；两者至少其一）。
	SecretSource string
	Secret       string
	// SubjectPrefix 可选主题前缀（如渠道标记）。
	SubjectPrefix string
	// Plain 明文模式（不套 TLS）：本地 SMTP sink 联调用（验收探针零外发硬性要求——
	// KI-001：默认运行外发连接数=0，真实外发仅 CORE_MAIL_E2E=1 显式开关；协议面/
	// 出站状态机断言走本机桩进程）。仅限回环桩会话，生产部署恒 TLS（声明面不变）。
	Plain bool
}

// NotifyParams Invoke 入参（JSON；op=notify 与 op=send-email 同构，notify 是别名入口）。
type NotifyParams struct {
	Subject string   `json:"subject"`      // 主题（RFC 2047 按需编码）
	Body    string   `json:"body"`         // 正文（text/plain UTF-8）
	To      []string `json:"to,omitempty"` // 本次收件人（缺省=Config.To）
	Cc      []string `json:"cc,omitempty"` // 可选抄送
}

// SendResult Invoke 出参（JSON）。
type SendResult struct {
	Op        string    `json:"op"`        // 实际执行的 op
	MessageID string    `json:"messageId"` // 本端生成的 Message-ID（审计回指）
	To        []string  `json:"to"`        // 实际收件人（含抄送）
	At        time.Time `json:"at"`        // 发送时刻（本端时钟）
}

// ErrUnknownOp op 未在该连接器 Ops() 声明内。
var ErrUnknownOp = errors.New("notify: op 未声明")

// FromEnvelope 出站信封 → 调用参数（cored 进程内自动投递的信封协议消费面：
// connectors.OutboundEnvelope——cored exec 出站桥经 stdin 投递的信封，subject/body
// 由桥内建渲染；本实现只消费这两个渲染字段，投递溯源字段留审计可回指查询面）。
func FromEnvelope(env connectors.OutboundEnvelope) NotifyParams {
	return NotifyParams{Subject: env.Subject, Body: env.Body}
}

// EmailConnector 通知邮件 OpsConnector（connectors.OpsConnector 实现）。
// 一次 Invoke 一次 SMTP 会话（无长连资源）；并发安全（无共享可变状态）。
type EmailConnector struct {
	cfg Config
	// dialTLS 测试注入点：缺省 tls.Dial；测试可换成明文连接打本地假 SMTP 服务。
	dialTLS func(network, addr string, cfg *tls.Config) (net.Conn, error)
}

// New 构建通知邮件连接器（无凭证配置也可安全构建——真实取值 Invoke/Probe 才解析，
// 声明/运行时分离 provider.md 规则 6）。
func New(cfg Config) *EmailConnector {
	if cfg.Port == 0 {
		cfg.Port = 465 // SSL 缺省端口
	}
	return &EmailConnector{cfg: cfg, dialTLS: dialTLSDefault}
}

// dialTLSDefault 缺省 TLS 拨号（implicit TLS；适配 net.Conn 签名便于测试注入）。
func dialTLSDefault(network, addr string, cfg *tls.Config) (net.Conn, error) {
	return tls.Dial(network, addr, cfg)
}

// Type ops 连接器类型（connectors.Type 词汇）。
func (c *EmailConnector) Type() string { return Type }

// Version 实现版本。
func (c *EmailConnector) Version() string { return Version }

// Ops 提供的 op 集（⊆ WorkService.tools 声明）。
func (c *EmailConnector) Ops() []string { return []string{OpNotify, OpSendEmail} }

// Invoke 执行一次通知 op（调用即效果：真实发出邮件，不可撤回）。
func (c *EmailConnector) Invoke(ctx context.Context, op string, params []byte) ([]byte, error) {
	if !connectors.HasOp(c, op) {
		return nil, fmt.Errorf("%w: %s", ErrUnknownOp, op)
	}
	var p NotifyParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("notify: params 解析失败: %w", err)
		}
	}
	if p.Subject == "" && p.Body == "" {
		return nil, errors.New("notify: subject/body 皆空")
	}
	to := p.To
	if len(to) == 0 {
		to = c.cfg.To // 收件人清单可配置（Provider 配置面缺省）
	}
	rcpts := dedup(append(append([]string{}, to...), p.Cc...))
	if len(rcpts) == 0 {
		return nil, errors.New("notify: 收件人为空（配置面与调用参数皆未给）")
	}
	secret, err := c.secret()
	if err != nil {
		return nil, err
	}
	subject := p.Subject
	if c.cfg.SubjectPrefix != "" {
		subject = c.cfg.SubjectPrefix + " " + subject
	}
	msgID, err := c.send(ctx, rcpts, subject, p.Body, secret)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(SendResult{Op: op, MessageID: msgID, To: rcpts, At: time.Now()})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Probe 通道可达性探测（TCP+TLS 建连+SMTP 握手+QUIT；不发送任何用户内容——
// Notification 登记「channel 可达性探测」notification.md §4 的机读落点）。
func (c *EmailConnector) Probe(ctx context.Context) error {
	secret, err := c.secret()
	if err != nil {
		return err
	}
	cl, err := c.dialClient(ctx, secret)
	if err != nil {
		return err
	}
	return cl.Quit()
}

// secret 解析授权码：Config.Secret 直给（部署侧注入/测试）优先；否则按 SecretSource
// 引用式取值（只支持 env: 前缀——vault:/keychain: 本实现不支持，显式报错不猜）。
func (c *EmailConnector) secret() (string, error) {
	if c.cfg.Secret != "" {
		return c.cfg.Secret, nil
	}
	src := c.cfg.SecretSource
	if v, ok := strings.CutPrefix(src, "env:"); ok && v != "" {
		v = os.Getenv(v)
		if v == "" {
			return "", fmt.Errorf("notify: secret 环境变量未设或为空（%s）", src)
		}
		return v, nil
	}
	return "", fmt.Errorf("notify: secret 无可用来源（声明面只允许 env:/vault:/keychain: 引用，实得 %q）", src)
}

// send 真实发送（RFC 5321 会话；返回本端 Message-ID）。
func (c *EmailConnector) send(ctx context.Context, rcpts []string, subject, body, secret string) (string, error) {
	cl, err := c.dialClient(ctx, secret)
	if err != nil {
		return "", err
	}
	defer cl.Close()
	if err := c.auth(cl, secret); err != nil {
		return "", err
	}
	if err := cl.Mail(c.cfg.From); err != nil {
		return "", fmt.Errorf("notify: MAIL FROM: %w", err)
	}
	for _, r := range rcpts {
		if err := cl.Rcpt(r); err != nil {
			return "", fmt.Errorf("notify: RCPT TO %s: %w", r, err)
		}
	}
	w, err := cl.Data()
	if err != nil {
		return "", fmt.Errorf("notify: DATA: %w", err)
	}
	msgID := fmt.Sprintf("<%d.notify@connector>", time.Now().UnixNano())
	if _, err := w.Write(buildMessage(c.cfg.From, rcpts, subject, body, msgID)); err != nil {
		return "", fmt.Errorf("notify: 写正文: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("notify: 正文提交: %w", err)
	}
	return msgID, cl.Quit()
}

func (c *EmailConnector) dialClient(ctx context.Context, secret string) (*smtp.Client, error) {
	if c.cfg.Host == "" || c.cfg.From == "" {
		return nil, errors.New("notify: 配置缺 host/from（Provider/WorkService 配置面注入）")
	}
	addr := net.JoinHostPort(c.cfg.Host, fmt.Sprint(c.cfg.Port))
	if c.cfg.Plain { // 明文模式：本地 sink 桩会话（探针零外发 KI-001；生产部署恒 TLS）
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("notify: 建连 %s: %w", addr, err)
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		cl, err := smtp.NewClient(conn, c.cfg.Host)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("notify: SMTP 握手: %w", err)
		}
		return cl, nil
	}
	tlsCfg := &tls.Config{ServerName: c.cfg.Host}
	conn, err := c.dialTLS("tcp", addr, tlsCfg)
	if err != nil {
		return nil, fmt.Errorf("notify: 建连 %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	cl, err := smtp.NewClient(conn, c.cfg.Host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("notify: SMTP 握手: %w", err)
	}
	return cl, nil
}

// auth AUTH PLAIN（服务端不宣 AUTH 能力时跳过——本地假服务/中继场景）。
func (c *EmailConnector) auth(cl *smtp.Client, secret string) error {
	if ok, _ := cl.Extension("AUTH"); !ok {
		return nil
	}
	return cl.Auth(smtp.PlainAuth("", c.cfg.From, secret, c.cfg.Host))
}

// buildMessage RFC 5322 报文（text/plain UTF-8；主题 RFC 2047 编码；正文 base64）。
func buildMessage(from string, rcpts []string, subject, body, msgID string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(rcpts, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", mimeQEncode(subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "Message-ID: %s\r\n", msgID)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(base64Wrap(body))
	return []byte(b.String())
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// 编译期断言：EmailConnector 满足工具调用域五方法契约。
var _ connectors.OpsConnector = (*EmailConnector)(nil)
