# 版本更新記錄

[English](CHANGELOG.md) · [简体中文](CHANGELOG.zh-CN.md) · **繁體中文** · [日本語](CHANGELOG.ja.md) · [Español](CHANGELOG.es.md)

本文件詳實記錄 Mailhearth 的所有重要版本演進與功能異動。

版本格式嚴格遵循 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) 規範，版本號命名遵循[語意化版本 2.0.0](https://semver.org/spec/v2.0.0.html)。

## [未發布]

### 變更（破壞性）— 計畫作為 0.2.0 發布

Mailhearth 尚未部署給真實使用者，因此本版本放棄對早期資料庫、設定及用戶端的全部相容性。

- **資料庫**：資料庫結構合併為單一 `0001_init.sql`，在一筆交易中套用（版本記錄於 `PRAGMA user_version`）。早期建置所建立的資料庫會在啟動時被拒絕，請以空的資料目錄啟動。移除了不再使用的舊資料表與舊欄位；郵件歷史（`message_state`、`mail_activity`）現在透過 `ON DELETE RESTRICT` 外部鍵而非觸發程序阻止刪除信箱；並補齊了缺少的外部鍵、`CHECK` 約束與索引。
- **設定**：移除了 `MAILHEARTH_PURELYMAIL_API_URL`、`MAILHEARTH_IMAP_ADDR`、`MAILHEARTH_IMAP_TLS`、`MAILHEARTH_SMTP_ADDR`、`MAILHEARTH_SMTP_TLS`、`MAILHEARTH_SIEVE_ADDR` 與 `MAILHEARTH_SIEVE_TLS`。新的安全預設值：一律需要初始化權杖（未設定 `MAILHEARTH_SETUP_TOKEN` 時自動產生並寫入日誌）；除非設定 `MAILHEARTH_SECURE_COOKIES=false`，否則啟用 `Secure` Cookie；`MAILHEARTH_UPLOAD_QUOTA_MB` 預設為 120 且一律強制執行；工作階段預設滑動有效期 14 天、絕對有效期 30 天。
- **HTTP API**：移除了單一連線與網域寫入端點的 `410 api_replaced` 佔位路由；`DELETE /api/mail/uploads/{id}` 對不屬於呼叫者的 id 回傳 404；`/api/setup/status` 移除了 `setupTokenRequired`；`Domain` 不再包含 `isShared`、`allowAccountReset`、`symbolicSubaddressing`、`dns`、`dnsCheckedAt` 與 `status`。
- **Web 用戶端**：升級至 Vite 8、Preact 11 與 TypeScript 7；API 型別由 Go 回應型別產生。
- **發行**：映像檔只針對 `vX.Y.Z` 標籤發布（`X.Y.Z` 與 `X.Y`），不再提供 `latest`；`docker-compose.yml` 固定使用 `0.2` 發行線。弱點掃描（govulncheck、npm audit）與 staticcheck 在 CI 中為阻斷項目。

### 新增與最佳化

- **五國語言在地化支援**：新增繁體中文（台灣）、日文及西班牙文的前台與管理介面完整翻譯，可在登入頁及個人設定頁自由切換，介面詞條與樣板參數透過 TypeScript AST 靜態測試達成 100% 覆蓋。
- **多服務商架構擴充**：新增對 Migadu 服務商與通用手動 IMAP/SMTP 郵件伺服器的連線支援，支援依連線獨立設定管理憑證與各協定端點範本。
- **外部憑證清理與查核工作流程**：針對外部服務商網路回應逾時或建立憑證中斷的情境，新增離線清理確認機制與管理員報告追蹤，杜絕殘留孤兒任務。
- **全套多語言文件重寫**：系統架構、維運手冊、安全規範、整合測試及專案說明文件全面人性化重構，五種語言實質內容高度對齊。

## [0.1.0] - 2026-09-20

首個正式公開版本：專為小型團隊量身打造的自架商務電子郵件平台，將郵件服務商帳號平順轉化為現代化的團隊協同郵件系統。

### 新增功能特性

- **首次執行與極簡引導精靈**：快速建立組織管理員，驗證郵件服務商 API 權杖，一鍵探索並唯讀匯入遠端既有網域、信箱與轉寄路由規則，全程不破壞遠端服務商原有設定。
- **以人為中心的團隊組織模型**：成員支援頭銜、部門、角色以及生命週期狀態（已邀請、正常活躍、已停用、已離職）；內建擁有者、管理員、一般成員角色，並支援基於具名權限點的自訂角色；所有敏感操作留存不可竄改的稽核記錄，支援平順移轉組織所有權。
- **信箱全生命週期管理**：支援個人專屬信箱與團隊共用信箱，精細分配「完整存取」、「可讀可寄」與「唯讀」權限；伺服器端統一為信箱安全產生與輪換應用程式專用密碼；支援一步式離職交接流程（Offboarding）——信箱移交接手人、轉為共用、封存保留、新信件轉寄、立即註銷所有線上工作階段及憑證安全撤銷。
- **彈性的郵件地址拓撲**：原生支援主地址、別名（Alias）、郵件轉寄（Forwarding）、全收（Catch-all）與前綴地址（Prefix），以及自動隨部門或團隊成員變動而動態同步的郵件群組發信清單。
- **網域管理與 DNS 健康監控**：支援多自訂網域綁定，視覺化即時監控 MX、SPF、DKIM 及 DMARC 的 DNS 解析健康狀態，並提供標準 DNS 設定記錄值一鍵複製功能。
- **現代響應式 Web 網頁信箱客戶端**：標準與自訂資料夾、分頁瀏覽、伺服器端極速全文檢索、星號標記、批次整理刪除、附件預覽與分塊下載、草稿即時自動儲存、豐富文字編輯與個人簽名檔、多重寄件身分自由切換、基於 IMAP IDLE 的即時桌面推播、全螢幕與鍵盤快速鍵操作、淺色與深色主題自由切換。
- **共用信箱團隊協同作業**：信件處理狀態一目了然——即時呈現哪位隊友已回覆、信件指派到人、協同標記解決狀態，以及新增僅團隊內部可見的私密備註；協同狀態統一錨定於固定 `Message-ID`，信件在資料夾之間移動後協作資料永久保留。
- **伺服器端篩選規則與自動回覆**：支援透過視覺化條件與動作設計郵件篩選規則，支援休假自動回覆；規則自動編譯為符合 RFC 規範的 Sieve 指令碼，並透過 ManageSieve 協定部署至伺服器端，即使所有成員離線也持續生效；接管既有指令碼前提供確認提示。
- **深度深度安全防禦**：所有服務商 API 憑證與協定密碼均採用 AES-256-GCM 高強度靜態加密，金鑰由伺服器端 master.key 安全衍生；基於 argon2id 的密碼強雜湊、嚴格的 CSRF 攔截與防暴力破解速率限制；郵件內文在伺服器端經過白名單深度消毒，呈現於嚴格 CSP 下的絕對無指令碼沙箱 iframe 中，預設封鎖外部追蹤圖片；附件強制以安全下載模式派送。
- **極簡單體部署體驗**：內嵌全部前端靜態資源的獨立 Go 二進位單一執行檔，基於純 Go 驅動程式 `modernc.org/sqlite` 的嵌入式 SQLite 儲存；配套輕量級 `Dockerfile` 與 `docker-compose.yml`，以及自動化 CI 管線。
- **完備的開發與測試套件**：內建處理序內模擬郵件服務商與展示資料環境（`make dev`）；自動化瀏覽器螢幕截圖測試指令碼；以及面向真實郵件服務商的端對端整合測試套件。

[未發布]: https://github.com/yll682/Mailhearth/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/yll682/Mailhearth/releases/tag/v0.1.0
