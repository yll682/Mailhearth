# 架構

[English](architecture.md) · [简体中文](architecture.zh-CN.md) · **繁體中文** · [日本語](architecture.ja.md) · [Español](architecture.es.md)

## 部署與元件

Mailhearth 使用單一 Go 執行檔、內嵌 Preact 資源和 SQLite（modernc，無 cgo）。
郵件伺服器保存內文並負責投遞和篩選。SQLite 保存組織資料、加密憑證、資源關聯、
管理操作、寄送請求和協作資料。Node 用於前端建置。

- `cmd/mailhearth`：設定、主金鑰、資料庫、IMAP 連線池、執行器及 HTTP server。
- `internal/config`、`internal/secrets`：環境設定、AES-256-GCM/HKDF、argon2id 和 token。
- `internal/db`、`internal/model`：內嵌 migration、關聯檢查及持久化模型。
- `internal/provider`：共用管理介面及 Purelymail、Migadu、manual 配接器。
- `internal/purelymail`：供對應配接器使用的 Purelymail API 用戶端。
- `internal/core`：連線、探索與匯入、組織、資源生命週期、Operation 和 Submission。
- `internal/mailproto/imappool`：連線額度、endpoint 版本失效處理及 IDLE 監聽。
- `internal/mailproto/mailops`、`mimeutil`、`sieve`：IMAP/SMTP、MIME 淨化、Sieve 編譯和 ManageSieve。
- `internal/httpapi`、`internal/web`、`web/`：權限檢查、JSON/SSE、內嵌資源及郵件、管理、設定介面。

## 組織與資源歸屬

每次部署有一個 Organization。Member 保存角色、部門及 `invited`、`active`、
`disabled`、`departed` 狀態。Role 包含具名權限。personal Mailbox 的所有權及 shared
Mailbox 的明確授權（`full`、`send`、`read`）決定郵件存取；管理權限不授予郵件存取。

MailConnection 屬於組織，保存供應商類型、名稱、管理 API 驗證、網域範圍和三項協定
預設範本。Domain 表示邏輯網域；DomainBinding 保存連線關聯、供應商設定及 DNS 狀態。
Mailbox 地址在所屬連線內唯一，不同連線的相同地址分別保存。

Address 包含 `primary`、`alias`、`forward`、`group`、`catchall`、`prefix` 和外部管理的
`external_rule`。信箱轉寄獨立保存於 `mailbox_forwardings`，匯入保留 primary 地址。
Identity 顯示設定和 SMTP 寄件授權分別處理；收件別名不授予對應 From 的寄送權限。
Group 計算全部符合條件的 personal 信箱，檢查連線與網域限制後移除重複目標。

ProviderResource 將各遠端參照關聯至一個本地物件及用途，保存歸屬、遠端狀態和
安全的結構化觀察結果。參照限制與 revision 檢查保護連線、組織範圍和歷史記錄。

## 協定設定

每個 Mailbox 有獨立的 IMAP、SMTP 和 ManageSieve endpoint。network mode 為
`inherit`、`override` 或 `disabled`；啟用的 endpoint 分別指定 username 和加密
Credential。憑證使用 `managed` 或明確輸入方式。候選設定通過全部啟用協定的驗證
之後才能在同一交易內提交。endpoint、連線、憑證及存取版本變化會關閉舊連線。

Purelymail 預設 IMAP 為 `imap.purelymail.com:993` TLS，SMTP 為
`smtp.purelymail.com:465` TLS，ManageSieve 為 `mailserver.purelymail.com:4190` STARTTLS。
Migadu 預設 IMAP 為 `imap.migadu.com:993` TLS，SMTP 為 `smtp.migadu.com:465` TLS，
ManageSieve 停用。手動連線預設全部停用，需要明確設定。TLS 使用系統憑證或明確指定
的私人 CA 憑證集合，並驗證 hostname。

## 探索、匯入與管理操作

探索完整讀取所選範圍，產生具有期限及連線 revision 的快照。匯入在同一本地交易內
檢查歸屬、版本、期限和相依項目。匯入的 personal 信箱沒有 owner 和登入憑證。同步
更新已登記資源的觀察結果；新增資源等待選擇匯入。owner、存取授權、協作資料、簽名
及 entered 憑證保留。

轉寄匯入保存來源信箱、所選目標、遠端參照及 `active`、`pending_confirmation`、
`blocked`、`unknown` 確認狀態。觀察目標及投遞方式與期望設定分別保存。未驗證的
方式保持 `unverified`；API 中存在資源及管理員報告均不證明實際投遞。Migadu 轉寄
寫入持續要求 V03 驗證。

Operation 保存 requestId、內容摘要、加密資料、資源占用及獨立步驟。重複提交傳回
原操作；版本變化及權限撤銷阻止舊設定執行。未知遠端寫入需要查核，已確認步驟保持
完成。外部操作保存管理員報告及 `systemVerified=false`。憑證建立回應遺失且沒有遠端
ID 時需要明確的清理報告。暫停立即撤銷本地存取；封存保留歷史。離職分別記錄移交、
群組處理和憑證撤銷。

## 郵件與寄送流程

1. HTTP 檢查目前所有權或授權，解析對應協定 endpoint。
2. IMAP 連線預設總上限 24、每個連線 8、每個信箱 3。IDLE 監聽執行時為一般請求保留
   總計 4 個、每個連線 2 個名額。
3. `mailops` 透過 IMAP 讀取資料夾、分頁、搜尋及 MIME 內容。明確映射、唯一
   SPECIAL-USE 和唯一名稱識別用於確定特殊資料夾。
4. `mimeutil` 清理 HTML/CSS、解析 `cid:`、阻擋遠端圖片。獨立文件使用限制性 CSP，
   在無指令碼的沙箱 iframe 中顯示。
5. Submission 保存 requestId、固定 Message-ID、內容摘要和加密 envelope；內文保存
   在伺服器 Drafts。SMTP accepted 及 Sent 副本分別記錄。未知寄送結果不會自動重試。
   重試副本保留 SMTP 狀態；草稿清理要求 UID EXPUNGE。
6. 共用 IDLE 監聽透過 SSE 通知瀏覽器。協作使用 `mid:<message-id>`，`message_state`
   保存負責人及狀態，`mail_activity` 保存回覆、轉寄、指派及備註，移動資料夾後保留。

## 規則與前端

結構化規則根據伺服器宣告的擴充功能編譯為 Sieve。ManageSieve 使用信箱 endpoint 和
go-managesieve。啟用前檢查目前指令碼 hash，接管需要確認，讀取獨立候選指令碼並查核
啟用結果，接著更新本地設定。既有指令碼保留。規則及自動回覆能力分別依賴啟用協定
和所需擴充功能。

Preact、`@preact/signals` 和 history router 提供 `/mail`、`/admin`、`/settings`、
`/login`、`/invite/:token`、`/setup`。860 px 以上使用三欄，以下使用抽屜和單欄。
English 來源文案提供 zh-CN、zh-TW、日本語和 Español 字典。TypeScript AST 檢查翻譯
覆蓋與插值參數。正式建置保留既有 hash 資源供已開啟的用戶端使用。

本地資料庫、HTTP 和建置檢查見[整合測試](integration-testing.zh-TW.md)。真實供應商
驗收尚未完成。
