# 安全規範與威脅防護指南

[English](security.md) · [简体中文](security.zh-CN.md) · **繁體中文** · [日本語](security.ja.md) · [Español](security.es.md)

## 威脅模型與保護資產

Mailhearth 在網路架構上坐落於團隊瀏覽器與上游郵件服務商（Purelymail、Migadu 或自建伺服器）之間。作為整合管理與日常信件收發的核心平台，系統所界定與保護的關鍵數位資產依敏感層級排列如下：

1. **各郵件連線的管理 API 憑證**：具備操作遠端網域、帳號指派與路由規則的最高系統權限；
2. **各信箱協定憑證**：用於 IMAP、SMTP 及 ManageSieve 實際收發信件與規則設定的密碼或應用程式專用密碼；
3. **郵件資料與組織通訊錄**：團隊的歷史郵件往來、草稿、共用信箱內部協作備註及團隊人員名冊；
4. **團隊成員的活動工作階段（Sessions）**：已登入成員的前端瀏覽器狀態與暫存驗證權杖。

### 潛在威脅與防禦邊界
Mailhearth 的安全架構針對以下主要攻擊面實施深度防禦：
- **網際網路外部攻擊者**：試圖透過弱密碼暴力破解、CSRF 跨站請求偽造或越權 API 竊取組織機密；
- **惡意/釣魚郵件發件人**：試圖透過特製的 HTML 郵件內文注入惡意 JavaScript 腳本（XSS）、觸發 CSS 釣魚覆蓋層，或透過內嵌遠端圖片探測成員真實 IP 與閱讀狀態；
- **已離職或未授權內部成員**：試圖保留未撤銷的存取管道，或越權窺探未分配給自己的個人信箱與機密共用信箱；
- **惡意外部客戶端**：試圖未經授權攔截或竊取第三方郵件應用程式專用密碼。

> **邊界說明**：實體主機伺服器或作業系統完全失陷不在軟體防護邊界內。但系統透過**全鏈路靜態加密**將受害範圍（Blast Radius）降至最低——即便資料庫檔案被整體竊取，在缺少獨立保管的 `master.key` 的情況下，攻擊者也絕對無法解密出任何明文憑證。

## 核心安全控制措施

### 1. 靜態憑證強加密（Secrets at Rest）
- **加密演算法**：由 `internal/secrets` 套件實作，採用業界公認的 AES-256-GCM 認證加密演算法。
- **金鑰衍生體系**：伺服器端的主加密金鑰（來自環境變數 `MAILHEARTH_MASTER_KEY` 或自動產生的 `<data>/master.key`，檔案權限嚴格設為 `0600`）透過 HKDF-SHA256 依業務情境衍生出完全隔離的專用子金鑰。
- **敏感資料零洩漏**：一般的管理 API 查詢絕不向前端回傳明文密碼或 API 權杖，僅呈現遮罩後的安全資訊。
- **一次性密碼安全領取（One-Time Claim Flow）**：當系統為外部客戶端產生新的應用程式密碼時，僅允許獲授權的管理員在有限的時效窗口內明確領取一次；回應標頭強制附加 `Cache-Control: no-store`，領取後伺服器記憶體立即銷毀明文暫存，防止重放或瀏覽器快取外流。

### 2. 身分驗證與工作階段防護（Authentication & Sessions）
- **現代密碼雜湊**：成員登入密碼採用 RFC 9106 推薦的 argon2id 演算法（參數調校為 `m=19MiB, t=2`），高抗 GPU/ASIC 離線暴力破解。
- **高強度工作階段權杖**：工作階段由 256 位元加密安全隨機數產生，資料庫僅儲存其 SHA-256 雜湊值；Cookie 嚴格附加 `HttpOnly`、`SameSite=Lax` 且在 HTTPS 環境下強制附加 `Secure`，採用 30 天滑動過期視窗。
- **防暴力破解速率限制**：對登入、邀請連結啟用等敏感驗證介面實施以 IP 與帳號為維度的多層速率限制。
- **離職立即撤銷**：成員一旦被管理員停用（`disabled`）或標記離職（`departed`），系統在單一本機交易中立即清空並註銷該成員的所有在線工作階段。

### 3. 細粒度權限模型與信箱隔離（Authorization）
- **權限正交隔離**：管理操作權限（如網域綁定、成員管理）與郵件閱讀權限完全脫鉤。具備管理員角色絕不代表擁有查閱團隊其他成員個人信箱的特權。
- **所有權與授權邊界**：所有郵件存取皆透過 `core.ResolveMailbox` 進行動態權限裁定，嚴格檢驗個人信箱所有權或共用信箱的授權層級（`read` 僅可查看不可加星號或寄信；`send` 可讀可寄但不可刪除或移動；`full` 具備完整管理權）。

### 4. 嚴密的全站 CSRF 防禦
- 所有能改變資料狀態的 HTTP 請求（POST、PUT、PATCH、DELETE）強制攜帶自訂標頭 `X-Requested-With: Mailhearth`。根據瀏覽器安全原則，未獲 CORS 明確授權的跨來源站台無法偽造該自訂標頭。
- 請求包含 `Origin` 標頭時，系統會嚴格比對是否與伺服器設定的 `MAILHEARTH_BASE_URL` 完全相符。

### 5. 不可信任郵件內文的深度沙箱防禦（Untrusted Content Sandboxing）
郵件是不可信內容的最常見來源，Mailhearth 實施了多階段深度消毒管線：
- **伺服器端標籤白名單消毒**：郵件 HTML 首先經過 `bluemonday` 嚴格過濾，接著進行 DOM 二次巡檢：徹底移除危險 CSS 偽協定（如 `expression`、`url()`、`position: fixed` 等釣魚樣式），將 `cid:` 內嵌圖片轉換為具驗證權限的端點，並強制外鏈加上 `target="_blank" rel="noopener noreferrer"`。
- **預設封鎖外部追蹤圖片**：所有外部引用的遠端圖片預設全面阻斷載入，徹底瓦解行銷追蹤標籤；僅在使用者明確點擊「顯示圖片」時載入。
- **無指令碼沙箱獨立呈現**：消毒後的郵件內文由獨立受保護的 URL 提供，強制回應標頭：
  ```http
  Content-Security-Policy: default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; script-src 'none'; form-action 'none'
  ```
  並於前端 `<iframe sandbox="allow-same-origin allow-popups ...">` 中呈現，**絕對不包含 `allow-scripts`**。即便面對極為罕見的未揭露 XSS 載荷，在無腳本沙箱與極嚴格 CSP 的雙重防線下，瀏覽器內核也絕不可能執行任何惡意程式碼。
- **無 Cookie 之短效 HMAC 檢視權杖**：iframe 的載入透過短效、綁定具體成員、信箱、資料夾與信件 UID 的 HMAC view token 進行驗證，完全不依賴 Cookie，防止越權探測。
- **附件安全派送**：所有附件強制附帶 `Content-Disposition: attachment` 與 `X-Content-Type-Options: nosniff` 標頭，除已確認安全的影像、純文字或 PDF 外，HTML、SVG、XML 等檔案堅決禁止內嵌執行。
- **信件撰寫與簽名對稱消毒**：使用者撰寫的內文與簽名檔在發往 SMTP 前，同樣經過完全相同的伺服器端消毒，防止本機惡意瀏覽器擴充套件污染寄出郵件。

### 6. 應用安全標頭基線
- 點擊劫持防護：全站預設套用 `X-Frame-Options: DENY`；
- 隱私資訊防洩：強制附帶 `Referrer-Policy: no-referrer`；
- 前端單頁應用專用 CSP：主頁面嚴格限制 `script-src 'self'`；
- 敏感介面快取防禦：所有 `/api/` 端點強制回傳 `Cache-Control: no-store`。

### 7. 協定連線與憑證安全
- 每個啟用的 IMAP、SMTP 及 ManageSieve 連線必須嚴格驗證伺服器憑證與主機名稱（SNI），預設使用作業系統信任鏈，亦支援載入專用私有 CA 憑證包。
- 系統**堅決不提供**任何「忽略憑證錯誤」或「跳過 TLS 檢查」的危險選項。

### 8. 不可竄改之稽核軌跡（Audit Trail）
- 舉凡組織架構變動、成員狀態異動、服務商連線調整、憑證輪換與權限變更，皆永久記錄於 `audit_log` 資料表中，詳實保存操作者 ID、來源 IP、時間戳記與完整異動快照。

## 部署安全實務建議

1. **強制啟用 HTTPS 並配置反向代理**：務必在前端反向代理（Caddy、Nginx）上配置完備的 TLS 憑證，並將 `MAILHEARTH_BASE_URL` 設定為 `https://...` 網址，以確保 Cookie 的 `Secure` 旗標生效；僅在代理伺服器受信任時開啟 `MAILHEARTH_TRUST_PROXY=true`。
2. **主加密金鑰（master.key）隔離保存**：切勿將 `/data/master.key` 與資料庫備份放置於同一個公開儲存空間。建議將金鑰妥善儲存於企業密碼管理器（如 1Password、Bitwarden）或專用硬體模組中。
3. **分配專用服務商 API 權杖**：在 Purelymail 或 Migadu 管理介面建立專供 Mailhearth 使用的獨立 API 憑證，切勿與其他系統共用，確保發生異常時能獨立撤銷。
4. **服務商主帳號啟用雙重驗證 (2FA)**：務必在郵件服務商官方控制台中為管理帳號啟用多因素驗證（2FA/TOTP），守護底層基礎設施的安全。
