package notify

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"strconv"

	"strings"
	"testing"

	"github.com/friddle-self-recursive-agent/core_system_workservice/connectors"
)

// fakeSMTP 最小 SMTP 会话桩（RFC 5321 主干：220/250/354/221；可选 AUTH PLAIN 广播）。
// 返回捕获的会话记录（命令与 DATA 正文），供断言。
type fakeSMTP struct {
	ln        net.Listener
	muCh      chan string // DATA 正文（含首部）
	commands  chan string // MAIL/RCPT/AUTH 行
	advertise bool        // 是否广播 AUTH PLAIN
	authSeen  chan string // AUTH PLAIN 参数（base64 \x00user\x00pass\x00）
	from      string      // 期望 MAIL FROM
}

func newFakeSMTP(t *testing.T, advertise bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{ln: ln, muCh: make(chan string, 64), commands: make(chan string, 8),
		advertise: advertise, authSeen: make(chan string, 2)}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeSMTP) addr() string { return f.ln.Addr().String() }
func (f *fakeSMTP) host() string { return "127.0.0.1" } // PlainAuth 明文连接仅对 localhost 放行

func (f *fakeSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.session(conn)
	}
}

func (f *fakeSMTP) session(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	say := func(s string) { w.WriteString(s + "\r\n"); w.Flush() }
	say("220 fake ESMTP ready")
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				say("250 OK queued")
				continue
			}
			select {
			case f.muCh <- line + "\n":
			default:
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "EHLO"):
			if f.advertise {
				say("250-fake greets")
				say("250 AUTH PLAIN")
			} else {
				say("250 fake greets")
			}
		case strings.HasPrefix(line, "HELO"), strings.HasPrefix(line, "NOOP"), strings.HasPrefix(line, "RSET"):
			say("250 OK")
		case strings.HasPrefix(line, "MAIL FROM:"):
			select {
			case f.commands <- line:
			default:
			}
			say("250 OK")
		case strings.HasPrefix(line, "RCPT TO:"):
			select {
			case f.commands <- line:
			default:
			}
			say("250 OK")
		case strings.HasPrefix(line, "AUTH PLAIN"):
			select {
			case f.authSeen <- line:
			default:
			}
			say("235 2.7.0 authentication succeeded")
		case line == "DATA":
			inData = true
			say("354 end with <CRLF>.<CRLF>")
		case line == "QUIT":
			say("221 fake closing")
			return
		default:
			say("500 unrecognized")
		}
	}
}

// plainConnector 构建指向假服务的连接器（明文注入替代 TLS dial）。
func plainConnector(t *testing.T, f *fakeSMTP, cfg Config, secret string) *EmailConnector {
	t.Helper()
	cfg.Host = f.host()
	cfg.From = "sender@deploy.local"
	cfg.To = []string{"default-rcpt@deploy.local"}
	cfg.Secret = secret
	c := New(cfg)
	p := f.addr()
	c.dialTLS = func(network, addr string, _ *tls.Config) (net.Conn, error) {
		return net.Dial(network, p) // 明文替换（测试注入点）
	}
	return c
}

func TestContractRegistration(t *testing.T) {
	c, ok := connectors.LookupOpsConnector(Type)
	if !ok {
		t.Fatalf("init 自注册未生效：%s 未在册", Type)
	}
	if c.Version() != Version || len(c.Ops()) != 2 {
		t.Fatalf("原型访问器不符: version=%s ops=%v", c.Version(), c.Ops())
	}
	if !connectors.HasOp(c, OpSendEmail) || !connectors.HasOp(c, OpNotify) {
		t.Fatalf("op 集不符")
	}
}

func TestInvokeSendEmailFlow(t *testing.T) {
	f := newFakeSMTP(t, true) // 广播 AUTH PLAIN
	c := plainConnector(t, f, Config{SubjectPrefix: "[core]"}, "secret-for-test")
	params, _ := json.Marshal(NotifyParams{Subject: "loop 摘要", Body: "本轮验收全绿", To: []string{"rcpt-a@deploy.local", "rcpt-a@deploy.local"}})
	out, err := c.Invoke(context.Background(), OpSendEmail, params)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	var res SendResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("result 解析: %v", err)
	}
	if res.Op != OpSendEmail || len(res.To) != 1 || res.To[0] != "rcpt-a@deploy.local" {
		t.Fatalf("result 不符: %+v", res)
	}
	if !strings.HasPrefix(res.MessageID, "<") {
		t.Fatalf("Message-ID 缺失: %s", res.MessageID)
	}
	// AUTH PLAIN base64(\x00user\x00pass\x00) 校验（凭据走 secret，测试值只在本测试内存）。
	authLine := <-f.authSeen
	payload := strings.TrimPrefix(authLine, "AUTH PLAIN ")
	decoded, _ := base64.StdEncoding.DecodeString(payload)
	if !strings.HasSuffix(string(decoded), "\x00sender@deploy.local\x00secret-for-test") {
		t.Fatalf("AUTH 载荷不符: %q", decoded)
	}
	// 首部与正文落报文（base64 正文可还原）。
	var msg strings.Builder
collect:
	for {
		select {
		case l := <-f.muCh:
			msg.WriteString(l)
		default:
			break collect
		}
	}
	body := msg.String()
	for _, want := range []string{"From: sender@deploy.local", "To: rcpt-a@deploy.local", "Message-ID:", "Content-Transfer-Encoding: base64"} {
		if !strings.Contains(body, want) {
			t.Fatalf("报文缺 %q：\n%s", want, body)
		}
	}
	// 非 ASCII 主题=RFC 2047 Q 编码（解码可还原原文，编码不改语义）。
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "Subject: ") {
			dec, derr := (&mime.WordDecoder{CharsetReader: func(cs string, r io.Reader) (io.Reader, error) { return r, nil }}).DecodeHeader(strings.TrimPrefix(line, "Subject: "))
			if derr != nil || dec != "[core] loop 摘要" {
				t.Fatalf("主题编码不可还原: %q %v", line, derr)
			}
			break
		}
	}
}

func TestInvokeNotifyAliasAndDefaultRecipient(t *testing.T) {
	f := newFakeSMTP(t, false) // 不广播 AUTH → 跳过认证路径
	c := plainConnector(t, f, Config{}, "s")
	out, err := c.Invoke(context.Background(), OpNotify, []byte(`{"subject":"s","body":"b"}`))
	if err != nil {
		t.Fatalf("Invoke notify: %v", err)
	}
	var res SendResult
	_ = json.Unmarshal(out, &res)
	if len(res.To) != 1 || res.To[0] != "default-rcpt@deploy.local" {
		t.Fatalf("默认收件人（配置面清单）不符: %v", res.To)
	}
	// 无参数覆盖时收件人取配置面——RCPT TO 命令应与之一致。
	got := <-f.commands
	if !strings.HasPrefix(got, "MAIL FROM:") {
		t.Fatalf("首命令应为 MAIL FROM，实得 %q", got)
	}
}

func TestInvokeErrors(t *testing.T) {
	f := newFakeSMTP(t, false)
	c := plainConnector(t, f, Config{}, "s")
	if _, err := c.Invoke(context.Background(), "webhook-send", []byte("{}")); !errors.Is(err, ErrUnknownOp) {
		t.Fatalf("未声明 op 应 ErrUnknownOp，实得 %v", err)
	}
	if _, err := c.Invoke(context.Background(), OpNotify, []byte(`{bad`)); err == nil || strings.Contains(err.Error(), "ErrUnknownOp") {
		t.Fatalf("坏 params 应解析错误，实得 %v", err)
	}
	if _, err := c.Invoke(context.Background(), OpNotify, []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "皆空") {
		t.Fatalf("空内容应报错，实得 %v", err)
	}
	c2 := New(Config{Host: "h", From: "f", To: []string{"r@deploy.local"}, SecretSource: "env:NO_SUCH_VAR_FOR_TEST"})
	if _, err := c2.Invoke(context.Background(), OpNotify, []byte(`{"subject":"s"}`)); err == nil || !strings.Contains(err.Error(), "NO_SUCH_VAR_FOR_TEST") {
		t.Fatalf("env 引用缺值应显式报错，实得 %v", err)
	}
	c3 := New(Config{Host: "h", From: "f", To: []string{"r@deploy.local"}, SecretSource: "vault:secret/x"})
	if _, err := c3.Invoke(context.Background(), OpNotify, []byte(`{"subject":"s"}`)); err == nil || !strings.Contains(err.Error(), "vault:") {
		t.Fatalf("不支持的取值来源应显式报错，实得 %v", err)
	}
}

func TestSecretFromEnv(t *testing.T) {
	t.Setenv("NOTIFY_TEST_SECRET", "abc")
	c := New(Config{Host: "h", From: "f", SecretSource: "env:NOTIFY_TEST_SECRET"})
	if s, err := c.secret(); err != nil || s != "abc" {
		t.Fatalf("env: 解析不符: %q %v", s, err)
	}
}

func TestProbeReachability(t *testing.T) {
	f := newFakeSMTP(t, false)
	c := plainConnector(t, f, Config{}, "s")
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("Probe 应可达: %v", err)
	}
	// 不可达：连不上的端口。
	bad := New(Config{Host: "127.0.0.1", Port: 1, From: "f", Secret: "s"})
	if err := bad.Probe(context.Background()); err == nil {
		t.Fatalf("不可达端口 Probe 应报错")
	}
}

// TestPlainModeLocalSink 明文模式（Config.Plain，KI-001 探针零外发）：不套 TLS 直打本地
// 桩——发送/探测全链路走同一会话语义（EXIT 0 + Message-ID + 桩收到报文），与 dialTLS
// 注入路径（既有测试）零交叉=配置面自身生效的机械判别。
func TestPlainModeLocalSink(t *testing.T) {
	f := newFakeSMTP(t, false)
	_, pn, _ := net.SplitHostPort(f.addr())
	port, _ := strconv.Atoi(pn)
	c := New(Config{Host: "127.0.0.1", Port: port, From: "sender@deploy.local",
		To: []string{"sink@local"}, Secret: "s", Plain: true})
	out, err := c.Invoke(context.Background(), OpSendEmail,
		[]byte(`{"subject":"sink 断言","body":"zero-egress"}`))
	if err != nil {
		t.Fatalf("明文模式 Invoke 应成功（本地桩）: %v", err)
	}
	var res SendResult
	if err := json.Unmarshal(out, &res); err != nil || res.MessageID == "" {
		t.Fatalf("回执形态错误: %s %v", out, err)
	}
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("明文模式 Probe 应可达: %v", err)
	}
}

func TestEncodingHelpers(t *testing.T) {
	if got := mimeQEncode("plain ascii"); got != "plain ascii" {
		t.Fatalf("ASCII 主题应原样: %q", got)
	}
	enc := mimeQEncode("中文主题")
	if !strings.HasPrefix(enc, "=?utf-8?q?") {
		t.Fatalf("非 ASCII 主题应 RFC 2047 编码: %q", enc)
	}
	wrapped := base64Wrap("正文内容")
	dec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(wrapped, "\r\n", ""))
	if err != nil || string(dec) != "正文内容" {
		t.Fatalf("base64 折行不可还原: %q %v", wrapped, err)
	}
}

// TestFromEnvelope 出站信封消费面：cored exec 出站桥信封（connectors.OutboundEnvelope）
// → 调用参数——subject/body 取信封渲染字段，溯源字段不进参数面（留审计）。
func TestFromEnvelope(t *testing.T) {
	env := connectors.OutboundEnvelope{
		Delivery: 3, Channel: "mail-chan", Origin: "route", TriggerSeq: 2,
		EventID: "ev-9", Type: "Escalate", DedupKey: "esc:wf:denied", Level: "error",
		Route: "root-agent", Owner: "global", At: "2026-11-24T00:00:00Z",
		Subject: "[cored] Escalate error mail-chan",
		Body:    "cored 进程内自动投递（受理后异步出站）\n事件类型: Escalate\n",
	}
	p := FromEnvelope(env)
	if p.Subject != env.Subject || p.Body != env.Body || p.To != nil || p.Cc != nil {
		t.Fatalf("信封映射不符: %+v", p)
	}
}
