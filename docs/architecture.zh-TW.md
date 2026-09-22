# 架構

[English](architecture.md) · [简体中文](architecture.zh-CN.md) · **繁體中文** · [日本語](architecture.ja.md) · [Español](architecture.es.md)

## 目標與非目標

Mailhearth 把 Purelymail 可靠且價格低廉的郵件基礎設施，包裝成一套小型組織能夠
部署、管理並且每天使用的產品。它刻意**不**執行 SMTP 伺服器、不儲存郵件的主要
副本、不過濾垃圾郵件，也不實作 DLP / eDiscovery / MDM。它同樣不是把 Purelymail
入口網站或另一個 Roundcube 重新包裝而成的產品：管理的基本單位是組織中的一個人，
帳號上的「使用者」並非管理單位。

影響設計的各種限制：

- **極小型主機。** 目標部署環境執行在能取得的最便宜 VPS 上。伺服器是單一靜態 Go
  執行檔，搭配 SQLite、行程內具上限的 IMAP 連線池，以及 51 KB（gzip 壓縮後）的
  Preact 前端。執行時期不需要 Redis、不需要 Postgres、不需要 Node，除了少數協程
  之外沒有背景工作程式。
- **Purelymail 是郵件的真實來源。** 郵件永遠不會複製到 Mailhearth 的資料庫。用戶端
  顯示的一切都透過 IMAP 隨需取得；資料庫只保存組織模型，以及以 `Message-ID` 為
  索引的少量協作中繼資料。
- **瀏覽器裡沒有機密。** API 權杖與信箱應用程式密碼以加密形式存放在 SQLite 中。
  瀏覽器只與 Mailhearth 通訊。

## 元件

| 套件 | 職責 |
|---|---|
| `cmd/mailhearth` | 進入點：組態、主金鑰、資料庫、IMAP 連線池、HTTP 伺服器 |
| `internal/config` | 環境組態 |
| `internal/db` | SQLite（modernc，不使用 cgo）與內嵌移轉 |
| `internal/secrets` | AES-256-GCM 加密盒（由主金鑰透過 HKDF 衍生）、argon2id、權杖 |
| `internal/purelymail` | 具型別的 API 用戶端；`fake/` 是記憶體中的 Purelymail |
| `internal/model` | 服務與 API 共用的組織型別 |
| `internal/core` | 服務：初始設定/匯入、成員、角色、網域、信箱、地址、群組、離職處理、團隊狀態 |
| `internal/mailproto/imappool` | 具上限的 IMAP 連線池與 IDLE 監看程式 |
| `internal/mailproto/mailops` | 資料夾、列表、呈現、動作、撰寫、SMTP |
| `internal/mailproto/mimeutil` | HTML 淨化器、文字/HTML 轉換、解碼 |
| `internal/mailproto/sieve` | 規則模型到 Sieve 的編譯器；ManageSieve 用戶端 |
| `internal/httpapi` | JSON API、工作階段、CSRF、上傳、SSE、沙箱化的郵件檢視 |
| `internal/web` | 內嵌 SPA，支援 gzip 與不可變快取 |
| `internal/devstack` | 供開發與測試使用的模擬 Purelymail、IMAP 與 SMTP |
| `web/` | Preact + Vite 單頁應用程式（郵件、管理、設定） |

## 組織模型

| 概念 | 含義 | 儲存於 |
|---|---|---|
| **Organization** | 一份安裝中唯一的租戶。 | `organizations` |
| **Member** | 實際登入 Mailhearth 的人員。具有角色、狀態（invited/active/disabled/departed）、職稱、部門。 | `members` |
| **Role** | 具名的權限集合（`members.manage`、`shared.manage`……）。內建：owner、admin、member；允許自訂角色。 | `roles` |
| **Domain** | Purelymail 帳號上的網域，附帶 DNS 健康狀態。 | `domains` ↔ Purelymail 網域 |
| **Mailbox** | 儲存郵件的登入帳號。`personal`（由單一成員擁有）或 `shared`（由組織擁有，由多位成員共同處理）。 | `mailboxes` ↔ Purelymail 使用者 |
| **Address** | 可接收郵件的對象：信箱本身的地址（`primary`）、指向單一信箱的 `alias`、轉寄到任意目標的 `forward`、`group` 的發送地址、`catchall` 或 `prefix` 規則。 | `addresses` ↔ Purelymail 路由規則 |
| **Identity** | 信箱可用來寄件的身分：From 地址 + 顯示名稱 + 簽名檔。 | `identities` |
| **Group** | 一組成員，可選擇搭配發送地址，其目標會跟隨成員組成。 | `groups`、`group_members` |
| **Access grant** | 成員 → 信箱，層級為 `full`/`send`/`read`。 | `mailbox_access` |

一位成員可以擁有數個信箱；一個信箱可以有多個地址；一個地址可以送達數位成員
（透過轉寄或群組）。當人員更換角色或離職時，信箱與地址仍留在組織內：擁有權會被
重新指派，永遠不會被隱含刪除。

## 對應到 Purelymail

| Mailhearth 動作 | Purelymail API 呼叫 |
|---|---|
| 連線 | `checkAccountCredit`（驗證權杖） |
| 匯入 / 同步 | `listDomains`、`listUser`、`listRoutingRules` — 唯讀、冪等 |
| 建立信箱 | `createUser`（隨機密碼，不寄送歡迎郵件）+ `createAppPassword` |
| 連線已匯入的信箱 | `createAppPassword`（永遠不需要既有密碼） |
| 輪替憑證 | `createAppPassword`，接著對舊的執行 `deleteAppPassword` |
| 為外部用戶端重設密碼 | `modifyUser{newPassword}` + 輪替 |
| 停權 / 離職鎖定 | `modifyUser{newPassword}` + `deleteAppPassword` |
| 別名 / 轉寄 / 全收地址 / 前綴 / 群組地址 | `createRoutingRule` / `deleteRoutingRule` |
| 信箱上的轉寄 | 在信箱本身地址上的路由規則（Purelymail 語意：規則優先於投遞） |
| 網域新增 / DNS 重新檢查 / 設定 | `addDomain`、`updateDomainSettings`、`getOwnershipCode` |

Mailhearth 為每個信箱保留恰好一個應用程式密碼，名稱為「Mailhearth」。成員永遠
看不到它；伺服器在確認成員擁有該信箱或取得授權之後，代替成員將它用於 IMAP、
SMTP 與 ManageSieve。管理權限不授予郵件存取權：讀取共用信箱一律需要明確的授權。

## 郵件路徑

1. `httpapi` 為已登入的成員解析信箱（`core.ResolveMailbox`），並取得憑證。
2. `imappool.Get` 傳回連線池中的連線（總數上限為 `MAILHEARTH_IMAP_MAX_CONNS`，
   每個憑證保留 2 條閒置連線，閒置 90 秒後回收）。
3. `mailops` 執行 IMAP 命令：資料夾使用帶 `LIST-STATUS` 的 `LIST`，分頁使用序號
   範圍的 `FETCH` 取得 envelope + flags + `BODYSTRUCTURE`，查詢使用 `UID SEARCH`，
   呈現使用 `BODY.PEEK[part]`，在可用時使用 `MOVE`/`UIDPLUS`，並具備後備做法。
4. HTML 內文會經過 `mimeutil.SanitizeHTML`（bluemonday 允許清單、CSS 清理、`cid:`
   解析、遠端圖片封鎖），並以具備 `default-src 'none'` CSP 的獨立文件提供，顯示於
   沙箱化的 iframe 中。
5. 寄送時使用 go-message 建構 RFC 5322，透過 SMTP 以相同憑證提交，接著附加到寄件
   備份並將原始郵件標記為已回覆/已轉寄。
6. IDLE 監看程式（每個信箱+資料夾一個，由所有開啟的分頁共用）透過 Server-Sent
   Events 將變更推送給瀏覽器。

## 共用信箱協作

協作狀態以 `mid:<message-id>` 為索引，因此在資料夾之間移動後仍然保留。
`message_state` 保存指派對象與開啟/已解決狀態；`mail_activity` 是僅可附加的記錄
（已回覆、已轉寄、已指派、備註……），由明確的團隊動作寫入，也會在成員從共用信箱
寄件時自動寫入。郵件列表會在資料列上加上「已由誰回覆」、指派對象與狀態標記。

## 規則

成員以結構化的條件/動作編輯郵件規則。`sieve.Compile` 會將它們（加上假期自動回覆）
轉換成 Sieve 指令碼，並使用 Purelymail 宣告支援的擴充功能
（`fileinto imap4flags copy body vacation`）。指令碼以 `mailhearth` 為名稱透過
ManageSieve（`mailserver.purelymail.com:4190`，STARTTLS）上傳並啟用。結構化形式
保存在 `mailboxes.settings_json`。

## 前端

Preact + `@preact/signals`，一個 60 行的 history router，不使用 UI 框架。路由：
`/mail/:mailbox/:folder/:uid`、`/admin/:section/:id`、`/settings/:tab`、`/login`、
`/invite/:token`、`/setup`。字串是英文鍵，搭配 zh-CN 字典。佈局在 860 px 以上是
三窗格的郵件用戶端，以下則是抽屜加單一窗格。
