# common_connector — 通用 connector（core_system）

跨部署复用的通用 connector 代码仓（R-ENG-3 分仓规则的"通用"类）：不绑单一部署用法，遵循 core_system_workservice 的契约层接口（依赖方向恒为 本仓 → workservice，本仓 go.mod 以工作区路径 replace 解析）。

## 首批内容

| connector | 用途 | 服务归属 |
|---|---|---|
| notify / send-email（通知 OpsConnector） | default user-system 通知能力：Event 报告默认通知支持 email（SMTP），收件人可配置 | default user-system |
| smtp | SMTP 通道实现（implicit TLS；凭据走 secret 引用 `env:QQ_SMTP_AUTH_CODE`，运行时值只存部署侧，R-PUB 规则 6） | 同上 |

## 契约（workservice connectors 包）

- **OpsConnector 五方法**（工具调用域，与资源域 ProviderImpl 两物）：`Type / Version / Ops / Invoke / Probe`。
- `notify` 包 init 时自注册（`connectors.RegisterOpsConnector`）；注册原型零配置可查（Type/Version/Ops），真实实例=部署侧装配注入配置面。
- op 词汇：`notify`（通用通知入口）/ `send-email`（邮件直达）；params/result 均为 JSON 字节。

## 布局

```
notify/            OpsConnector 实现（SMTP 邮件）
  notify.go        连接器主体（Invoke/Probe/secret 引用式解析）
  email.go         报文编码辅助（RFC 2047 主题 / base64 正文）
  register.go      契约注册（init 自注册）
  notify_test.go   假 SMTP 服务会话桩测试（7 例）
cmd/send-email/    部署侧直调入口（探针/装配两用 CLI；真实值全在部署侧环境/旗标）
```

## 配置面（Provider/WorkService 声明，运行时值部署侧）

| 项 | 声明面 | 运行时实例 |
|---|---|---|
| SMTP 主机/端口 | 占位符（`<smtp-host>:<port>`，SSL） | 部署侧 Provider 配置 |
| 发件账号 | 占位符 | 部署侧 Provider 配置 |
| 收件人清单 | 可配置（Provider 配置面 `to:`） | 部署侧默认值 |
| 授权码 | `secret: {source: "env:QQ_SMTP_AUTH_CODE"}` | 部署侧 env（0600），值绝不入仓 |

## 红线

- 公开仓禁 IP/内网域名/真实路径/凭据取值/厂商选型名（core_design rule.md R-PUB-1，checker：core_design lint/personal-info.sh）。
- Provider 运行时实例（真实账号密码）只存部署侧（.env/本地配置），声明面只写 `secret: {source: "env:..."}` 引用。
- 工作区 git 走 git@github.com（ssh.github.com:443 经代理），凭据由部署侧 .ssh/config 提供。
