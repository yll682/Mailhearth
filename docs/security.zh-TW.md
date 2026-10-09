# 安全性

[English](https://github.com/yll682/Mailhearth/blob/main/docs/security.md) · [简体中文](https://github.com/yll682/Mailhearth/blob/main/docs/security.zh-CN.md) · **繁體中文** · [日本語](https://github.com/yll682/Mailhearth/blob/main/docs/security.ja.md) · [Español](https://github.com/yll682/Mailhearth/blob/main/docs/security.es.md)

## 威脅模型

Mailhearth 位於員工瀏覽器與 Purelymail、Migadu 或手動設定的郵件伺服器之間。管理憑證可操作帳戶資源，協定憑證存取各信箱。需要保護的資產包括：

1. 各連線的管理 API 憑證。
2. 用於 IMAP/SMTP/ManageSieve 的獨立信箱密碼。
3. 郵件內容與組織通訊錄。
4. 成員的工作階段。

納入考量的攻擊者包括：網際網路上的攻擊者、惡意或釣魚郵件、已離職的員工，以及試圖讀取未獲授權信箱的成員。主機本身遭到入侵不在防護範圍內，只做影響範圍的限制（機密資料加密存放，因此單獨拿到資料庫副本沒有用處）。

## 防護措施

**靜態存放的機密。** `secrets.Box` 以 AES-256-GCM 密封數值，金鑰由本次安裝的主鑰匙經 HKDF-SHA256 衍生而來。主鑰匙來自 `MAILHEARTH_MASTER_KEY`，或首次啟動時產生的 `<data>/master.key`（權限 0600）。不同用途使用不同的衍生金鑰。已保存的 API 和協定憑證僅傳回遮蔽資訊。新產生的外部用戶端密碼允許授權管理員在期限內明確領取一次；領取回應使用 `Cache-Control: no-store`，並移除可領取的秘密。

**身分驗證。** 成員密碼使用 argon2id（19 MiB，t=2）。工作階段是 256 位元的隨機權杖，以 SHA-256 雜湊後存放；Cookie 帶 `HttpOnly`、`SameSite=Lax`，基礎 URL 為 HTTPS 時帶 `Secure`，到期採 30 天滑動制。登入與接受邀請按 IP 做速率限制。停用或辦理離職會立即撤銷該成員的所有工作階段。

**授權。** 每個管理端點都會檢查成員角色上的一項權限；僅限擁有者的操作（轉移）要求 `org.owner`。郵件端點透過 `core.ResolveMailbox` 解析信箱，這要求擁有權或明確的存取授權，並強制執行授權層級（`read` 不能加旗標也不能寄送；`send` 不能刪除或移動）。管理權限永遠不代表可以存取郵件。

**CSRF。** 會改變狀態的 `/api` 請求必須帶上 `X-Requested-With: Mailhearth`（瀏覽器在沒有 CORS 的情況下無法跨來源加上這個標頭），而且當 `Origin` 標頭存在時，它必須與主機相符。

**不受信任的郵件內容。**
- HTML 在伺服器端以允許清單（bluemonday）消毒，再經過一次 DOM 處理：移除危險的 CSS（`expression`、`url()`、`position:fixed`），把 `cid:` 圖片改寫成通過驗證的分段 URL，除非使用者要求否則阻擋遠端圖片，並對連結強制加上 `target=_blank rel=noopener noreferrer`。
- 消毒後的文件從自己的 URL 提供，附帶 `Content-Security-Policy: default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'none'; form-action 'none'`，並顯示在 `<iframe sandbox="allow-same-origin allow-popups …">` 之中，不含 `allow-scripts`。保留 `allow-same-origin` 只是為了讓父頁面量測高度；無論如何 CSP 都保證裡面無法執行任何指令碼。
- iframe 以一個短效的 HMAC 檢視權杖驗證，該權杖綁定成員、信箱、資料夾與 UID，因此不需要 Cookie。
- 附件一律以 `Content-Disposition: attachment` 提供，除非型別屬於可安全內嵌的型別（點陣圖片、PDF、音訊與視訊、text/plain）；HTML、SVG 與 XML 一律不內嵌顯示。所有回應都帶 `X-Content-Type-Options: nosniff`。
- 撰寫的 HTML 與簽名檔在寄出前會經過同一套消毒流程，因此即使瀏覽器工作階段遭到入侵，也無法把指令碼注入郵件。
- 郵件與附件大小有上限（`MAILHEARTH_MAX_UPLOAD_MB`、`MAILHEARTH_MAX_MESSAGE_MB`）；超過 2 MB 的文字分段在顯示時會被截斷。

**應用程式標頭。** `X-Frame-Options: DENY`、`Referrer-Policy: no-referrer`、給 SPA 用的 CSP（`script-src 'self'`）、只對帶雜湊的資源使用不可變快取、API 回應一律 `no-store`。

**連線與憑證。** 管理驗證與協定驗證分別設定。候選設定通過全部啟用 endpoint 的驗證後提交。連線、endpoint、憑證及存取版本使舊連線和請求失效。暫停立即撤銷本地存取並保留憑證。managed 輪替分別記錄建立、驗證、提交和撤銷；entered 憑證及外部用戶端需要明確的供應商側處理。遠端撤銷須通過相應查核才能確認；管理員報告保存 `systemVerified=false`。憑證建立回應遺失且沒有遠端 ID 時需要清理報告，不會自動再次建立憑證。

**關聯與並行。** 資料庫限制檢查組織、連線、信箱及憑證歸屬。requestId、資源占用及 expectedRevision 保護管理操作；排隊、執行及操作控制檢查權限。刪除會影響寄送或協作歷史的信箱保留封存。domain scope 控制管理與探索，郵件存取另行檢查。

**TLS 與授權。** 各項啟用協定驗證 hostname 及憑證，使用系統憑證或明確設定的私人 CA。協定沒有忽略憑證驗證選項。Identity 顯示設定與寄件授權分別處理；別名、匯入及轉寄登記不授予 SMTP From 權限。

**寄送與遠端觀察。** Submission 加密 envelope 並保留固定識別。SMTP 與 Sent 分別保存；未知寄送結果需要查核，重新寄送需要明確的新請求。轉寄匯入保留確認狀態及未驗證的投遞方式。API 資源存在與外部報告均不確認實際投遞。

**Sieve。** 規則由結構化模型編譯而來，編譯時驗證標頭名稱、大小、地址與旗標；使用者無法提交原始 Sieve。ManageSieve 檢查擴充功能、active script hash、獨立候選指令碼的讀取及啟用結果。接管需要確認，既有指令碼保留。未明確的驗證拒絕保持未驗證。

**稽核。** 每一項管理變更都會連同操作者、目標與詳細內容記錄到 `audit_log`。

## 部署建議

- 在反向代理上終結 TLS，並把 `MAILHEARTH_BASE_URL` 設成 HTTPS 來源，這樣 Cookie 才會帶 `Secure`。只有在代理會設定 `X-Forwarded-For` 時，才設定 `MAILHEARTH_TRUST_PROXY=true`。
- 把 `master.key` 與資料庫分開備份；將它保存在你的密碼管理器中。沒有它，憑證需要透過適用的 entered 或 managed 流程重新設定並驗證。
- 為 Mailhearth 設定專用管理 API 憑證，以便獨立撤銷。
- 在供應商帳戶啟用兩步驟驗證；API 憑證可獨立存取帳戶，因此需要專門保護。
