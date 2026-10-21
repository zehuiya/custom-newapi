# 渠道端点开关本地运行测试

2026-10-21 在本机从当前源码构建并启动网关后，通过真实 HTTP 请求完成 **51/51** 项端到端验证。网关地址为 `http://127.0.0.1:3000`，上游为脚本启动的 `http://127.0.0.1:33091`，仅监听本机回环地址。

使用独立 SQLite 数据库 `/tmp/onesapi-endpoint-runtime.cASIC9/test.db`，未连接项目原有数据库或任何外部 AI 服务。启用内存渠道缓存，`SYNC_FREQUENCY=2`，关闭 Redis；测试管理员、令牌和渠道均通过正常 API 创建。

| 验证内容 | 数量 | 结果 |
| --- | ---: | --- |
| 三种端点的配置字段缺省时，默认选择高优先级渠道 | 3 | 通过 |
| 关闭高优先级渠道当前端点后，选择次优先级渠道 | 3 | 通过 |
| 各端点开关独立，不影响另两种请求 | 6 | 通过 |
| 流式请求跳过已关闭当前端点的渠道 | 3 | 通过 |
| 显式指定已关闭当前端点的渠道，返回 503，上游零请求 | 3 | 通过 |
| 所有渠道关闭当前端点，普通请求返回 503，上游零请求 | 3 | 通过 |
| 所有渠道关闭当前端点，流式请求返回 503，上游零请求 | 3 | 通过 |
| 重新开启后立即恢复高优先级选渠 | 3 | 通过 |
| 显式指定重新开启的渠道，请求成功 | 3 | 通过 |
| 上游真实返回 500 后，普通重试跳过已关闭的中间优先级渠道 | 3 | 通过 |
| 上游真实返回 500 后，显式兜底跳过列表中已关闭的渠道 | 3 | 通过 |
| Claude 原生 Messages 默认支持、关闭零请求、重新开启流式 | 3 | 通过 |
| 亲和性缓存建立、保持选渠，以及端点关闭覆盖既有亲和性 | 9 | 通过 |
| 浏览器保存的独立渠道：Messages 和 Chat Completions 关闭，Responses 保持开启 | 3 | 通过 |

这里的三种端点为 `/v1/messages`、`/v1/chat/completions` 和 `/v1/responses`。OpenAI 渠道也验证了客户端 Messages 转换成上游 Chat Completions 的路径：限制始终依据客户端请求端点生效。

重试与兜底用例中，上游记录均为 `high (HTTP 500) → low (HTTP 200)`；已关闭当前端点的 `middle` 没有收到请求。亲和性用例中，先绑定 `high`，再将 `middle` 调整为更高优先级，确认请求仍到 `high`；关闭 `high` 的当前端点后，请求立即转到 `middle`。

所有成功请求均检查返回的模拟回答；流式请求额外检查 SSE 响应头和数据。拒绝用例同时检查 HTTP 状态和上游调用记录，避免仅根据错误响应判断通过。

浏览器界面验证另行新建 `endpoint-ui-test`（渠道 ID `5`，模型 `gpt-4o`），确认三个开关默认开启，分别关闭 Messages 和 Chat Completions 后保存；刷新并重新编辑，仍显示 Responses 开启、另两个开关关闭；渠道列表对应的两个关闭标识也正确。随后真实请求同一渠道：Messages 和 Chat Completions 均返回 503，mock 上游零请求；Responses 返回 200，mock 记录命中 `ui`。HTTP 测试保留了这条渠道的浏览器保存值。截图保存于 `/tmp/onesapi-endpoint-runtime.cASIC9/endpoint-switch-ui.jpg`。

初次调试时遇到创建后缓存同步窗口，以及并行操作 SQLite 的短暂 `SQLITE_BUSY`。脚本现于渠道创建后等待缓存同步，并仅对可重复执行的配置 `PUT` 在数据库锁定时短暂重试。最终完整一轮 51 项验证全部通过，未修改产品代码。测试结束后恢复自动化测试渠道的默认开关、原始优先级及亲和性配置，并清空测试亲和性缓存。

可复用脚本：`tests/endpoint_switch_e2e/run_e2e.py`。仅依赖 Python 标准库，运行示例：

```sh
python3 tests/endpoint_switch_e2e/run_e2e.py \
  --base-url http://127.0.0.1:3000 \
  --report /tmp/onesapi-endpoint-switch-e2e-report.json \
  --hold-mock
```

基础脚本包含 48 项；在浏览器中完成上述独立渠道设置后，加上 `--verify-ui` 可复现全部 51 项。应在空白测试数据库上运行，网关配置应包括 `SYNC_FREQUENCY=2`。脚本拒绝外网网关地址及含无关渠道的实例，输出与 JSON 报告均不含 API 令牌或上游密钥。详细运行记录保存在 `/tmp/onesapi-endpoint-switch-e2e-report.json`。
