# 維運

[English](operations.md) · [简体中文](operations.zh-CN.md) · **繁體中文** · [日本語](operations.ja.md) · [Español](operations.es.md)

## 容量規劃

Mailhearth 是為買得到的最小主機設計的。

| 資源 | 典型值 | 說明 |
|---|---|---|
| 執行檔 | 約 25 MB 靜態 | 沒有 cgo，沒有執行階段相依套件 |
| 閒置 RSS | 25–40 MB | `GOMEMLIMIT` 預設為 160 MiB |
| 忙碌 RSS（10 位使用者） | 60–120 MB | 主要來自 IMAP 抓取緩衝區 |
| CPU | 可忽略 | 登入時的 argon2id 是最重的一步 |
| 磁碟 | 隨組織資料增加 | SQLite 儲存組織、操作、寄送狀態與協作資料；郵件儲存在所屬 IMAP 服務 |
| 前端 | gzip 後約 100 KB | 2026-10-09 的 JS 與 CSS 建置結果；檔案名稱包含內容摘要 |

把 `MAILHEARTH_IMAP_MAX_CONNS`（預設 24）調整到大約「同時使用的使用者人數 × 2
加上大家保持開啟的信箱數量」。全域預設上限為 24，每個連線上限為 8，每個信箱
上限為 3。IDLE 使用獨立配額，保留一般請求的連線配額。遵守各服務商的實際限制。

## 備份

全部狀態就是資料目錄：

| 路徑 | 內容 |
|---|---|
| `/data/mailhearth.db` | SQLite 資料庫（WAL 模式） |
| `/data/mailhearth.db-wal` | 預寫日誌；要與資料庫一起複製 |
| `/data/master.key` | 加密已儲存憑證的金鑰 |
| `/data/uploads/` | 等待寄出的撰寫附件（暫時性） |

請在容器停止時備份，或使用 `sqlite3 mailhearth.db ".backup out.db"` 取得一致的
線上複本。把 `master.key` 保存在另一個安全的位置。在新主機上還原時：放好這兩個
檔案，啟動執行檔即可。

## 升級

migration 在啟動時自動執行，包括資料表轉換與關聯檢查。升級前保存資料庫與
master key 的一致備份。拉取新映像檔，執行
`docker compose up -d`。不支援跨移轉降低版本；請改為還原備份。

## 反向代理

Caddy：

```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1      # required for Server-Sent Events
    }
}
```

nginx：在 `/api/mail/` 上設定 `proxy_buffering off;` 和
`proxy_read_timeout 3600s;`，讓 SSE 串流不會被緩衝；附件則設定
`client_max_body_size 50m`。

## 故障排除

**`provider_auth_failed`** — 在管理 → 連線中更新所屬連線的管理認證。候選認證失敗
時保留目前可用設定；郵件協定的認證分別檢查。

**信箱協定尚未設定** — 匯入只登記選中的資源。明確設定 IMAP、SMTP、ManageSieve
及 entered 認證，或使用該連線提供的 managed 認證能力。每個啟用協定通過實際認證
後才儲存設定。SMTP 與 ManageSieve 可以分別停用。

**`mailbox_auth_failed`** — 檢查所屬協定的使用者名稱與密碼。entered 認證透過協定
設定更新；managed 認證透過輪換操作更新。網路錯誤、暫時拒絕及未明確分類的
ManageSieve 回應保留未確認狀態。遠端撤銷需要使用新的連線檢查。

**規則無法儲存** — 檢查信箱 ManageSieve endpoint、TLS 與伺服器擴充功能。
Purelymail 預設使用 `mailserver.purelymail.com:4190` 和 STARTTLS；Migadu 模板
預設停用，啟用時填寫帳戶實際設定。接管既有 active script 需要明確確認與目前內容
摘要；自動回覆另外需要 `vacation` 擴充功能。

**Operation 為 `unknown`** — 讀取步驟記錄並核查遠端結果，確認後才能明確重試。
資源占用會保留。新增認證回應遺失且沒有遠端 ID 時，請在服務商介面處理認證，隨後
提交管理員清理報告。報告取消原操作並保留資源：`external_reported`，
`systemVerified: false`。

**Submission 為 `sent_copy_failed`** — SMTP 已接受郵件，只重試 Sent 副本。
SMTP 或 APPEND 結果為 `unknown` 時，查詢 Submission 與唯一提交標識，保留 requestId。

**同步發現新增資源** — 在所屬連線的結果中選擇需要匯入的資源。同步保留認證、
授權和歷史；讀取失敗不會據此判定資源已刪除。

**沒有即時更新** — SSE 被代理伺服器緩衝了，請見上文。此時用戶端會退回手動
重新整理，而且在發生操作時仍會輪詢資料夾。

**超大資料夾的第一頁很慢** — 列表是一次序號範圍 FETCH，取 50 封郵件的信封與
內文結構。Purelymail 的伺服器端搜尋索引（按使用者啟用）能讓搜尋變快；
Mailhearth 建立的信箱預設為開啟。

## 日誌

結構化文字日誌輸出到 stderr。`MAILHEARTH_LOG_LEVEL=debug` 會加上每個請求的
記錄行和 IMAP 監看器重新連線的訊息。日誌中沒有任何憑證。

## 檢查命令

```powershell
go test ./... -run '^$'
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./internal/mailproto/sieve ./internal/provider -run '^TestMultiProvider'
go vet ./...
npm --prefix web run test
npm --prefix web run build
```

真實環境測試需要關閉 `MAILHEARTH_DEV_STACK`。認證設定放在專案
`data/integration/multi-provider/` 目錄，設定 `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG`，
執行 `go test -count=1 -v ./internal/integration/multiprovider`。缺少設定會明確失敗。
本機資料庫檢查與真實驗收分別記錄；尚未執行的真實驗收項目保持未完成狀態。
