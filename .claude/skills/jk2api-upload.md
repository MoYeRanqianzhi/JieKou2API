---
name: jk2api-upload
description: Upload JieKou login tokens to a JK2API crowdfunding pool. Use when you have a JieKou cookie token and want to contribute it to get an API key, or when building automation for bulk token upload.
---

# JK2API Token Upload

Upload JieKou AI login tokens to a JK2API reverse proxy pool.

## Prerequisites

- A JK2API server URL (e.g. `https://jk.mosoft.icu`)
- A JieKou `token` cookie value (obtained after logging into jiekou.ai)

## How to get the token

1. Log in at `https://jiekou.ai/user/login` (GitHub OAuth supported)
2. Open browser console (F12) and run:
   ```js
   document.cookie.match(/token=([^;]+)/)?.[1]
   ```
3. Copy the output — that's the token

## API Endpoints

### Donate (upload + get API key)

```bash
curl -X POST https://SERVER/public/donate \
  -H "Content-Type: application/json" \
  -d '{"token": "TOKEN_VALUE", "label": "OPTIONAL_LABEL"}'
```

Response:
```json
{"ok": true, "label": "my-account", "api_key": "sk-ant-api01-xxx"}
```

### Upload only (no API key returned)

```bash
curl -X POST https://SERVER/public/upload \
  -H "Content-Type: application/json" \
  -d '{"token": "TOKEN_VALUE", "label": "OPTIONAL_LABEL"}'
```

Response:
```json
{"ok": true, "label": "my-account"}
```

## Parameters

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `token` | string | Yes | JieKou `token` cookie value |
| `label` | string | No | Account identifier (auto-generated if empty) |

## Notes

- Both JSON and `application/x-www-form-urlencoded` are accepted
- The `label` field is sanitized (only alphanumeric, `-`, `_`, `.`)
- Tokens are saved to the server's `auths/` directory
- The `/public/donate` endpoint generates a `sk-ant-api01-xxx` format API key
- Generated API keys can be used with `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_BASE_URL`
