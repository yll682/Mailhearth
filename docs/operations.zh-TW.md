# 系統維運與生產部署指南

[English](operations.md) · [简体中文](operations.zh-CN.md) · **繁體中文** · [日本語](operations.ja.md) · [Español](operations.es.md)

## 容量規劃與資源耗用

Mailhearth 專為超低資源耗用的輕量化主機設計，即使在市面上規格最小的入門級 VPS 上也能穩定順暢運作。

| 資源維度 | 典型佔用 | 維運說明 |
|---|---|---|
| 可執行二進位檔 | 約 25 MB 靜態檔案 | 零 cgo 相依，無需外部動態函式庫或環境執行階段 |
| 閒置常駐記憶體 (RSS) | 25–40 MB | 預設啟用 Go 記憶體軟上限 `GOMEMLIMIT=160MiB`，防止突發峰值 |
| 忙碌常駐記憶體 (10 人並行) | 60–120 MB | 記憶體主要由 IMAP 批次抓取快取與 MIME 解析緩衝區佔用 |
| CPU 運算開銷 | 日常趨近於 0% | 最密集的運算僅發生於使用者登入時的 argon2id 密碼雜湊推導 |
| 磁碟儲存成長 | 僅隨團隊組織資料成長 | SQLite 保存組織、權限、操作紀錄與團隊協作備註；郵件內文永久留存於服務商端 |
| Web 前端資源大小 | gzip 後約 100 KB | 包含完整 Webmail 與管理後台，檔案名稱內含內容指紋雜湊，支援長期快取 |

### IMAP 連線池與並行調校

透過環境變數 `MAILHEARTH_IMAP_MAX_CONNS`（系統預設值為 24）調校全域並行連線池。建議評估公式：`預估同時在線人數 × 2 + 團隊常態保持開啟的信箱總數`。

系統內部實施精細的分級配額保護：
- **全域連線池上限**：預設 24 個連線；
- **單一服務商連線上限**：每個郵件連線最多佔用 8 個並行連線；
- **單一信箱上限**：每個信箱最多複用 3 個連線；
- **IDLE 監聽保障**：背景 IMAP IDLE 即時推播監聽器佔用獨立配額，在 IDLE 運作時始終為前台一般互動請求保留至少 4 個全域插槽及單一連線 2 個插槽。

> **提示**：調整時請務必核對上游郵件服務商（如 Migadu 或 Purelymail）對單一 IP 或單一帳號設定的最大並行連線數限制。

## 設定項參考

所有設定項皆透過環境變數設定。設定值格式錯誤或超出範圍時，系統會在啟動時報錯並指明對應變數；被自動調整的設定值會以警告記錄，啟動時也會輸出生效設定（不含機密）。

| 變數 | 預設值 | 用途 |
| --- | --- | --- |
| `MAILHEARTH_BASE_URL` | 空 | 對外公開 URL，用於產生邀請連結。使用 `https://` 位址時會啟用 `Secure` Cookie。 |
| `MAILHEARTH_LISTEN` | `:8080` | HTTP 監聽位址。 |
| `MAILHEARTH_DATA_DIR` | `./data`（容器內為 `/data`） | SQLite 資料庫、主金鑰、上傳檔案與遷移前備份。 |
| `MAILHEARTH_MASTER_KEY` | 自動產生到 `<data>/master.key` | 32 位元組金鑰（base64 或 hex），用於加密儲存的憑證。 |
| `MAILHEARTH_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error`。 |
| `MAILHEARTH_TRUST_PROXY` | `false` | 從受信代理讀取 `X-Forwarded-For` / `X-Forwarded-Host`。 |
| `MAILHEARTH_TRUSTED_PROXIES` | 本機迴路與私有網段 | 反向代理的 CIDR 或位址，以逗號分隔。來自其他對端的轉送標頭會被忽略。 |
| `MAILHEARTH_SECURE_COOKIES` | `MAILHEARTH_BASE_URL` 為 `https://` 時為 `true` | 強制開啟或關閉 Cookie 的 `Secure` 屬性。 |
| `MAILHEARTH_ALLOWED_MAIL_NETWORKS` | 空 | 允許郵件伺服器所在的 CIDR。除非列在此處，否則系統會拒絕連線至私有、迴路與鏈路本機位址；IMAP/SMTP/ManageSieve 伺服器位於內網時需要設定。 |
| `MAILHEARTH_CA_BUNDLES_FILE` | 空 | 將 CA 憑證包編號對應到 PEM 檔案的 JSON 檔，例如 `{"1": "/etc/mailhearth/ca.pem"}`。協定端點透過編號引用以信任私有 CA。 |
| `MAILHEARTH_IMAP_MAX_CONNS` | `24` | IMAP 連線上限。連線池支援 4 到 24，超出範圍的設定值會被調整並記錄警告。 |
| `MAILHEARTH_IMAP_IDLE_SECONDS` | `90` | 閒置 IMAP 連線在多少秒後關閉（10 到 3600）。 |
| `MAILHEARTH_MAX_UPLOAD_MB` | `25` | 單一附件上傳的最大大小。 |
| `MAILHEARTH_MAX_MESSAGE_MB` | `40` | 編碼後郵件的最大大小。附件在寄送編碼後會增大約三分之一。 |
| `MAILHEARTH_UPLOAD_QUOTA_MB` | 3 × `MAILHEARTH_MAX_MESSAGE_MB` | 單一成員未寄出上傳檔案的總大小。 |
| `MAILHEARTH_SESSION_DAYS` | `30` | 工作階段滑動過期時間。 |
| `MAILHEARTH_SESSION_MAX_DAYS` | `90` | 工作階段絕對有效期，不得短於 `MAILHEARTH_SESSION_DAYS`。 |
| `MAILHEARTH_INVITE_DAYS` | `7` | 邀請連結有效天數（1 到 90）。 |
| `MAILHEARTH_DEV_STACK` | `false` | 僅供開發：行程內模擬服務商、IMAP 與 SMTP。 |
| `GOMEMLIMIT` | 未設定時為 `160MiB` | Go 堆積記憶體上限。 |

`MAILHEARTH_PURELYMAIL_API_URL`、`MAILHEARTH_IMAP_ADDR`、`MAILHEARTH_IMAP_TLS`、`MAILHEARTH_SMTP_ADDR`、`MAILHEARTH_SMTP_TLS`、`MAILHEARTH_SIEVE_ADDR` 與 `MAILHEARTH_SIEVE_TLS` 只會在升級「郵件連線」功能出現之前建立的安裝時讀取一次，並轉換為其第一個連線。除此之外，連線與協定端點皆在管理後台中設定。

## 資料備份與災難復原

Mailhearth 的完整執行階段狀態皆儲存於持久化掛載的資料目錄（預設為 `/data`）中：

| 檔案 / 路徑 | 作用與說明 | 備份需求 |
|---|---|---|
| `/data/mailhearth.db` | 系統核心 SQLite 資料庫（運作於 WAL 預寫日誌模式） | 核心資料 |
| `/data/mailhearth.db-wal` | 預寫日誌暫存檔，包含最新寫入交易 | 必須與 `.db` 同步備份 |
| `/data/master.key` | 主加密金鑰，用於加解密儲存於資料庫中的 API 憑證與協定密碼 | 核心機密，需異地安全保存 |
| `/data/uploads/` | 信件撰寫器中暫存的待寄送附件 | 暫存檔案，重啟後可自動重建 |

### 線上熱備份方案
推薦使用 SQLite 內建的線上安全備份機制，無需停機即可產生交易一致性複本：
```bash
docker compose exec mailhearth sqlite3 /data/mailhearth.db ".backup /data/backup_$(date +%Y%m%d_%H%M%S).db"
```

### 冷備份方案
停止容器後直接封存資料目錄：
```bash
docker compose stop
tar -czvf mailhearth-backup-$(date +%Y%m%d).tar.gz /data/mailhearth.db /data/mailhearth.db-wal /data/master.key
docker compose start
```

### 異地災難復原程序
在全新伺服器上恢復運作僅需三個步驟：
1. 準備好目標伺服器的 Docker 環境；
2. 將備份的 `mailhearth.db`（或熱備份產生的 `.db` 檔案重新命名為 `mailhearth.db`）與 `master.key` 放置於新伺服器的資料目錄中；
3. 啟動 `docker compose up -d`，系統啟動時將自動識別資料、驗證主金鑰並平順接手運作。

## 系統升級與資料庫遷移

Mailhearth 內建了宣告式的資料庫遷移引擎（`internal/db/migrations`）。

1. **自動遷移機制**：當啟動新版本二進位檔時，系統會自動偵測現有資料庫結構版本，依序執行所需的資料表演進、外部鍵關聯重建與資料完整性檢驗。修改既有資料庫之前，系統會在同一目錄寫入一份副本，命名為 `mailhearth.db.pre-<遷移名稱>-<時間戳記>.bak`。
2. **升級前準備**：在拉取新版本映像檔前，務必依前述步驟先完成一次完整的資料庫熱備份與 `master.key` 備份。
3. **執行升級**：
   ```bash
   git pull
   docker compose pull
   docker compose up -d --build
   ```
4. **版本還原說明**：Mailhearth 的資料庫遷移僅支援向前相容。若升級後遇到不可預期的狀況需要還原舊版本，**不支援跨遷移版本直接降級執行**；請切換回舊版本映像檔，並還原升級前儲存的資料庫備份。也可以直接還原自動產生的 `.bak` 副本；確認升級無誤後可刪除舊的 `.bak` 副本。

## 反向代理與 TLS 設定

生產環境中，Mailhearth 前端應部署反向代理（如 Caddy 或 Nginx）負責對外公開網域名稱存取與 TLS 憑證自動化終結。

### Caddy（強烈推薦）
Caddy 能夠全自動申請與展期 Let's Encrypt 憑證，設定簡潔：
```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        # 必須停用緩衝以確保 Server-Sent Events (SSE) 即時推播順暢
        flush_interval -1
    }
}
```

### Nginx
若使用 Nginx，必須特別注意關閉 SSE 推播串流緩衝並設定合適的逾時時間：
```nginx
server {
    listen 443 ssl http2;
    server_name mail.example.com;

    ssl_certificate /path/to/fullchain.pem;
    ssl_certificate_key /path/to/privkey.pem;

    # 允許上傳較大附件
    client_max_body_size 50m;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 針對即時推播介面停用緩衝
        proxy_buffering off;
        proxy_cache off;
        proxy_read_timeout 3600s;
        proxy_send_timeout 3600s;
    }
}
```

並在 `.env` 中設定 `MAILHEARTH_TRUST_PROXY=true`，以確保稽核日誌與速率限制使用真實的存取端 IP。若反向代理並非從本機迴路或私有網段連線，請將其位址加入 `MAILHEARTH_TRUSTED_PROXIES`。

## 常見問題診斷與應變措施

### 1. 服務商管理連線報錯：`provider_auth_failed`
- **原因**：服務商 API 權杖過期、已被重設或存取權限不足。
- **處理**：登入「管理主控台 → 郵件連線」，點擊編輯並更新該連線的管理 API 權杖。在更新未成功驗證前，系統將安全保留當前現有設定，不會影響正常運作。

### 2. 信箱顯示「協定尚未設定」
- **原因**：初次匯入資源僅擷取了遠端中繼資料，並未自動包含登入密碼。
- **處理**：進入該信箱的設定頁面，分別設定 IMAP、SMTP 及 ManageSieve 的協定參數與憑證。系統在儲存前會執行真實的連線驗證測試，僅在所有啟用的協定均握手成功時才會原子化更新。若無須寄信或不支援 Sieve，可直接停用相應協定。

### 3. 信箱協定連線報錯：`mailbox_auth_failed`
- **原因**：對應協定的使用者名稱、信箱密碼或應用程式專用密碼填寫有誤，或服務商端已修改密碼。
- **處理**：若為手動輸入模式（`entered`），在信箱協定設定視窗中直接輸入新密碼；若為代管憑證（`managed`），在管理主控台發起憑證輪換操作。

### 4. 郵件篩選規則無法儲存
- **原因**：信箱的 ManageSieve 協定端點未設定、TLS 握手失敗，或伺服器缺少關鍵 Sieve 擴充功能。
- **處理**：
  - Purelymail 預設應為 `mailserver.purelymail.com:4190` 並啟用 STARTTLS；
  - Migadu 的 ManageSieve 模板預設停用，若需使用請手動開啟並填寫正確的帳號設定；
  - 若伺服器上已存在活躍的篩選指令碼，系統為保護既有邏輯會提示確認視窗，請明確確認接管；
  - 若儲存休假自動回覆，請確認伺服器支援 `vacation` 擴充指令。

### 5. 非同步管理任務（Operation）顯示為 `unknown`
- **原因**：向遠端郵件服務商寫入資料時遭遇連線中斷或逾時，無法確定遠端是否已成功建立資源。
- **處理**：
  - 檢視該操作的詳細分步紀錄，系統會保留資源互斥鎖以防止狀態進一步脫節；
  - 若建立憑證中斷且未回傳遠端憑證 ID，請登入服務商官方控制台，檢查並刪除可能殘留的應用程式密碼；
  - 清理完畢後，在 Mailhearth 介面中點擊「提交憑證清理報告」，系統將安全取消該掛起任務，狀態記錄為 `external_reported`，`systemVerified=false`，同時完整保留先前已就緒的其他資源。

### 6. 寄件狀態顯示為 `sent_copy_failed`
- **原因**：信件已被底層 SMTP 伺服器接受並開始遞送，但在將副本寫入寄件匣（Sent 資料夾）時 IMAP 發生短暫網路抖動。
- **處理**：無須擔心重複寄信！系統具備嚴格的狀態解耦機制，直接點擊「重新儲存副本」即可，系統僅重新執行 IMAP 追加儲存，絕不會重複向收件人進行 SMTP 投遞。

### 7. 頁面收信沒有即時推播，必須手動重新整理
- **原因**：反向代理伺服器緩衝了 Server-Sent Events (SSE) 資料流。
- **處理**：請檢查前文的反向代理設定，確保 Caddy 啟用了 `flush_interval -1`，或 Nginx 針對 `/api/mail/` 介面關閉了 `proxy_buffering`。

### 8. 超大資料夾首次載入較慢
- **說明**：Mailhearth 在開啟資料夾時執行單次區間 FETCH 批次讀取 50 封郵件的摘要中繼資料與 MIME 結構。在 Purelymail 環境中，系統預設會為新建立的信箱開啟伺服器端全文搜尋索引，以保障長久極速體驗。

## 系統日誌與可觀測性

Mailhearth 輸出標準結構化文字日誌至 `stderr`，可直接透過容器日誌工具收集：
```bash
docker compose logs -f --tail=100 mailhearth
```

- 預設日誌等級為 `info`；
- 若需診斷網路信號交換與 IMAP 監聽斷線重連細節，可在 `.env` 中設定 `MAILHEARTH_LOG_LEVEL=debug`；
- 每個 API 回應都帶有 `X-Request-ID` 標頭，錯誤日誌中的 `requestId` 與之相同；伺服器端錯誤記錄為 `error` 層級，其他用戶端錯誤記錄為 `info` 層級；
- **安全保障**：所有日誌輸出均經過安全脫敏，絕不輸出任何使用者明文密碼、API 密鑰或解密憑據。

## 常用測試與自我檢查指令

```powershell
# 執行全部基準測試
go test ./... -run '^$'

# 執行多服務商模型與核心領域測試
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./internal/mailproto/sieve ./internal/provider -run '^TestMultiProvider'

# 程式碼靜態品質檢查
go vet ./...

# 前端自動化測試與正式建置打包檢驗
npm --prefix web run test
npm --prefix web run build
```

如需對真實環境進行自動化整合測試，請關閉 `MAILHEARTH_DEV_STACK`，在 `data/integration/multi-provider/` 中依規範提供真實憑證並設定 `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG`，隨後執行 `go test -count=1 -v ./internal/integration/multiprovider`。
