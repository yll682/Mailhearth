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
| 磁碟 | 幾 MB | SQLite：組織模型與協作中介資料；郵件留在 Purelymail |
| 前端 | gzip 後 51 KB | 一個 JS 分塊、一個 CSS 檔案，不可變快取 |

把 `MAILHEARTH_IMAP_MAX_CONNS`（預設 24）調整到大約「同時使用的使用者人數 × 2
加上大家保持開啟的信箱數量」。每個 IDLE 監看器占用一個連線。Purelymail 允許
每位使用者建立相當多的 IMAP 連線，不過沒有理由持有超過需要的數量。

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

移轉會在啟動時自動執行，而且都是追加式的。拉取新映像檔，執行
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

**「Purelymail 拒絕了這個 API 權杖」** — 權杖已被撤銷或輸入錯誤。請在
管理 → 連線 底下更換。

**某個信箱顯示「未連接」** — 匯入的信箱只有在有人綁定（成員加入流程）或管理員
按下 *連接* 時才會取得應用程式密碼。如果 Purelymail 拒絕
（`createAppPassword` 失敗），請確認該使用者仍存在於帳戶中，然後執行
*立即同步*。

**「郵件伺服器拒絕了此信箱憑證」** — 應用程式密碼已在 Purelymail 入口網站中
刪除，或使用者的密碼在 Mailhearth 之外被重設。請對該信箱使用 *輪換憑證*。

**規則無法儲存** — 主機必須能連上 `mailserver.purelymail.com:4190` 的
ManageSieve（STARTTLS）。部分 VPS 服務商會封鎖對外連接埠；請用
`openssl s_client -starttls sieve -connect
mailserver.purelymail.com:4190` 測試。

**沒有即時更新** — SSE 被代理伺服器緩衝了，請見上文。此時用戶端會退回手動
重新整理，而且在發生操作時仍會輪詢資料夾。

**超大資料夾的第一頁很慢** — 列表是一次序號範圍 FETCH，取 50 封郵件的信封與
內文結構。Purelymail 的伺服器端搜尋索引（按使用者啟用）能讓搜尋變快；
Mailhearth 建立的信箱預設為開啟。

## 日誌

結構化文字日誌輸出到 stderr。`MAILHEARTH_LOG_LEVEL=debug` 會加上每個請求的
記錄行和 IMAP 監看器重新連線的訊息。日誌中沒有任何憑證。

## 開發環境的模擬實作

`MAILHEARTH_DEV_STACK=1` 會把 Purelymail、IMAP 和 SMTP 換成處理程序內的模擬
實作。`-seed-demo` 會加入兩個網域、五位使用者、路由規則和範例郵件。模擬的
API 權杖是 `dev-token`；使用者用 `<name>-pass` 驗證。Go 測試使用同一套模擬
實作，因此 `go test ./...` 會走過完整路徑，包括 IMAP IDLE、SMTP 提交和
HTML 消毒。
