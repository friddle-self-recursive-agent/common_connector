module github.com/friddle-self-recursive-agent/common_connector

go 1.26.0

require github.com/friddle-self-recursive-agent/core_system_workservice v0.0.0

// 部署工作区六仓并置布局（R-ENG-3 依赖方向恒为 本仓 → workservice 契约层；
// replace 只解析到工作区相邻路径，不改变依赖方向，也不引入网络取用）。
replace github.com/friddle-self-recursive-agent/core_system_workservice => ../core_system_workservice
