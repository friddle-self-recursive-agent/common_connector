// register.go —— 契约注册（部署侧装配入口）。
// import 本包即自注册（契约约定：驱动包 init 时 RegisterOpsConnector）；
// 注册的是零配置原型（发现面：Type/Version/Ops 可查，Invoke/Probe 无配置时显式报错）。
// 真实实例=部署侧装配 New(Config{...}) 构造（配置面注入，不进仓）。
package notify

import (
	"github.com/friddle-self-recursive-agent/core_system_workservice/connectors"
)

func init() { connectors.RegisterOpsConnector(New(Config{})) }
