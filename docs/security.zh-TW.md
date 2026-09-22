# 安全性

[English](security.md) · [简体中文](security.zh-CN.md) · **繁體中文** · [日本語](security.ja.md) · [Español](security.es.md)

## 威脅模型

Mailhearth 位於員工瀏覽器與 Purelymail 之間，手上握有一個可以建立、刪除和重設
帳號上任何信箱的 API 權杖。依照敏感程度排列，需要保護的資產是：

1. Purelymail API 權杖。
2. 為了存取 IMAP/SMTP/Sieve 而保管的信箱應用程式密碼。
3. 郵件內容與組織通訊錄。
4. 成員的工作階段。

納入考量的攻擊者包括：網際網路上的攻擊者、惡意或釣魚郵件、已離職的員工，以及
試圖讀取未獲授權信箱的成員。主機本身遭到入侵不在防護範圍內，只做影響範圍的
限制（機密資料加密存放，因此單獨拿到資料庫副本沒有用處）。

## 防護措施

**靜態存放的機密。** `secrets.Box` 以 AES-256-GCM 密封數值，金鑰由本次安裝的
主鑰匙經 HKDF-SHA256 衍生而來。主鑰匙來自 `MAILHEARTH_MASTER_KEY`，或首次
啟動時產生的 `<data>/master.key`（權限 0600）。不同用途使用不同的衍生金鑰。
API 權杖在儲存後只以遮蔽過的提示顯示，應用程式密碼則從不顯示。

**身分驗證。** 成員密碼使用 argon2id（19 MiB，t=2）。工作階段是 256 位元的
隨機權杖，以 SHA-256 雜湊後存放；Cookie 帶 `HttpOnly`、`SameSite=Lax`，基礎
URL 為 HTTPS 時帶 `Secure`，到期採 30 天滑動制。登入與接受邀請按 IP 做速率
限制。停用或辦理離職會立即撤銷該成員的所有工作階段。

**授權。** 每個管理端點都會檢查成員角色上的一項權限；僅限擁有者的操作
（轉移）要求 `org.owner`。郵件端點透過 `core.ResolveMailbox` 解析信箱，這
要求擁有權或明確的存取授權，並強制執行授權層級（`read` 不能加旗標也不能
寄送；`send` 不能刪除或移動）。管理權限永遠不代表可以存取郵件。

**CSRF。** 會改變狀態的 `/api` 請求必須帶上 `X-Requested-With: Mailhearth`
（瀏覽器在沒有 CORS 的情況下無法跨來源加上這個標頭），而且當 `Origin` 標頭
存在時，它必須與主機相符。

**不受信任的郵件內容。**
- HTML 在伺服器端以允許清單（bluemonday）消毒，再經過一次 DOM 處理：移除
  危險的 CSS（`expression`、`url()`、`position:fixed`），把 `cid:` 圖片改寫成
  通過驗證的分段 URL，除非使用者要求否則阻擋遠端圖片，並對連結強制加上
  `target=_blank rel=noopener noreferrer`。
- 消毒後的文件從自己的 URL 提供，附帶
  `Content-Security-Policy: default-src 'none'; img-src 'self' data:; style-src
  'unsafe-inline'; script-src 'none'; form-action 'none'`，並顯示在
  `<iframe sandbox="allow-same-origin allow-popups …">` 之中，不含
  `allow-scripts`。保留 `allow-same-origin` 只是為了讓父頁面量測高度；無論
  如何 CSP 都保證裡面無法執行任何指令碼。
- iframe 以一個短效的 HMAC 檢視權杖驗證，該權杖綁定成員、信箱、資料夾與
  UID，因此不需要 Cookie。
- 附件一律以 `Content-Disposition: attachment` 提供，除非型別屬於可安全內嵌的
  型別（點陣圖片、PDF、音訊與視訊、text/plain）；HTML、SVG 與 XML 一律不內嵌
  顯示。所有回應都帶 `X-Content-Type-Options: nosniff`。
- 撰寫的 HTML 與簽名檔在寄出前會經過同一套消毒流程，因此即使瀏覽器工作階段
  遭到入侵，也無法把指令碼注入郵件。
- 郵件與附件大小有上限（`MAILHEARTH_MAX_UPLOAD_MB`、
  `MAILHEARTH_MAX_MESSAGE_MB`）；超過 2 MB 的文字分段在顯示時會被截斷。

**應用程式標頭。** `X-Frame-Options: DENY`、`Referrer-Policy: no-referrer`、
給 SPA 用的 CSP（`script-src 'self'`）、只對帶雜湊的資源使用不可變快取、
API 回應一律 `no-store`。

**Purelymail 憑證。** 每個信箱一個應用程式密碼。移交、轉為共用、暫停與離職
處理都會輪換它，同時重設 Purelymail 密碼，讓離職者在手機與桌面用戶端上設定
的連線停止運作。舊的應用程式密碼會在上游撤銷。

**Sieve。** 規則由結構化模型編譯而來，編譯時驗證標頭名稱、大小、地址與旗標；
使用者無法提交原始 Sieve。

**稽核。** 每一項管理變更都會連同操作者、目標與詳細內容記錄到 `audit_log`。

## 部署建議

- 在反向代理上終結 TLS，並把 `MAILHEARTH_BASE_URL` 設成 HTTPS 來源，這樣
  Cookie 才會帶 `Secure`。只有在代理會設定 `X-Forwarded-For` 時，才設定
  `MAILHEARTH_TRUST_PROXY=true`。
- 把 `master.key` 與資料庫分開備份；將它保存在你的密碼管理器中。沒有它，
  已存放的憑證必須重新建立（產品可以做到：輪換每個信箱）。
- 使用一個專門給 Mailhearth 的 Purelymail API 權杖，這樣才能獨立撤銷它。
- 在 Purelymail 帳號本身啟用兩步驟驗證；API 權杖會繞過它，這也是它名列
  首要資產的原因。
