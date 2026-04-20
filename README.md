# JieKou2API

将 [JieKou AI](https://jiekou.ai) 免费试用模型反代为 Anthropic / OpenAI 兼容 API，可直接用于 Claude Code。

## 特性

- **双协议兼容** — 同时支持 Anthropic Messages API (`/v1/messages`) 和 OpenAI (`/v1/chat/completions`)
- **多账号轮询** — 令牌池 round-robin + 熔断器自动隔离故障账号
- **热重载** — `config.yaml` 和 `auths/` 目录变更后自动生效，无需重启
- **众筹登录** — 公开页面引导用户贡献 JieKou 登录态，自动发放 API Key
- **Admin 面板** — 令牌池状态、API Key 管理、配置编辑
- **官方 API 回退** — 未知 API Key 自动透传至 JieKou 官方 API

## 快速开始

### Docker 部署（推荐）

```bash
git clone https://github.com/MoYeRanQianZhi/JieKou2API.git
cd JieKou2API
cp config.example.yaml config.yaml
# 编辑 config.yaml，设置 api_keys 等
echo -n "your-admin-token" > token.key
docker compose up -d
```

### 手动编译

```bash
go build -o jiekou2api .
cp config.example.yaml config.yaml
./jiekou2api
```

## 配置

```yaml
server:
  listen: ":8080"
  api_keys:            # 客户端访问所需的 API Key
    - "sk-ant-api01-xxx"

upstream:
  base_url: "https://jiekou.ai"
  default_model: "claude-opus-4-7"

auth:
  dir: "auths"         # 登录态 JSON 文件目录
  watch_interval: 15s
  breaker:
    threshold: 3       # 连续失败次数后熔断
    cooldown: 1h       # 熔断冷却时间

limits:
  global_rpm: 0        # 0 = 不限
  account_rpm: 0
  client_rpm: 0
```

## API 端点

| 端点 | 说明 |
|------|------|
| `POST /v1/messages` | Anthropic Messages API（流式/非流式） |
| `POST /v1/chat/completions` | OpenAI Chat Completions API |
| `GET /v1/models` | 模型列表 |
| `GET /health` | 健康检查 |
| `/admin/` | 管理面板（需 token.key） |
| `/login.html` | 众筹登录页 |
| `POST /public/contribute` | 贡献登录态接口 |

## 使用方式

### Claude Code

在 `settings.json` 中添加：

```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "sk-ant-api01-xxx",
    "ANTHROPIC_BASE_URL": "https://your-domain.com"
  }
}
```

### 通用

```bash
curl -X POST https://your-domain.com/v1/messages \
  -H "x-api-key: sk-ant-api01-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-opus-4-7",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

## 添加登录态

将 JieKou 的 cookie token 保存为 JSON 文件至 `auths/` 目录：

```json
{
  "token": "JieKou-cookie-token-value",
  "label": "account-name"
}
```

或通过 `/login.html` 众筹页面自动完成。

## 许可

MIT
