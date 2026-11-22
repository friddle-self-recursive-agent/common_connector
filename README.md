# common_connector — 通用 connector（core_system）

跨部署复用的通用 connector 代码仓（R-ENG-3 分仓规则的"通用"类）：不绑单一部署用法，遵循 core_system_workservice 的契约层五方法接口（依赖方向恒为 本仓 → workservice）。

## 首批内容

| connector | 用途 | 服务归属 |
|---|---|---|
| notify / send-email（通知 OpsConnector） | default user-system 通知能力：Event 报告默认通知支持 email（SMTP），收件人可配置 | default user-system |
| smtp | SMTP 通道实现（SSL；凭据走 secret 引用 `env:QQ_SMTP_AUTH_CODE`，运行时值只存部署侧，R-PUB 规则 6） | 同上 |

## 红线

- 公开仓禁 IP/内网域名/真实路径/凭据取值/厂商选型名（core_design rule.md R-PUB-1，checker：core_design lint/personal-info.sh）。
- Provider 运行时实例（真实账号密码）只存部署侧（.env/本地配置），声明面只写 `secret: {source: "env:..."}` 引用。
- 工作区 git 走 git@github.com（ssh.github.com:443 经代理），凭据由部署侧 .ssh/config 提供。
