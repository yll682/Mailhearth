# 系統架構指南

[English](architecture.md) · [简体中文](architecture.zh-CN.md) · **繁體中文** · [日本語](architecture.ja.md) · [Español](architecture.es.md)

## 整體設計與模組元件

Mailhearth 採用極簡且高效率的單體架構設計，後端採用 Go 語言開發，前端使用輕量級響應式框架 Preact，資料庫採用嵌入式 SQLite（以純 Go `modernc.org/sqlite` 驅動程式實作，完全零 cgo 相依）。所有前端正式環境靜態檔案均透過 Go 的 `embed` 機制打包封裝於單一執行檔中。

在職責邊界的切分上：
- **外部郵件伺服器**：專注於協定層級的網路傳輸、郵件遞送、底層資料夾樹狀目錄與 RFC 822 格式郵件內文實體儲存。
- **Mailhearth 核心系統**：作為管理與應用層，持久化維護組織架構、使用者與角色、加密憑證儲存、資源對應關係、非同步管理操作、信件寄送紀錄以及團隊協作狀態資料。

程式碼庫各核心元件職責劃分如下：
- `cmd/mailhearth`：系統啟動入口點，負責環境組態載入、主金鑰衍生與校驗、資料庫遷移執行、IMAP 連線池初始化、背景非同步工作協程配置與 HTTP/SSE 伺服器監聽。
- `internal/config`、`internal/secrets`：環境設定驗證；採用 AES-256-GCM 與 HKDF 進行多層金鑰衍生；argon2id 密碼雜湊運算及權杖生命週期管理。
- `internal/db`、`internal/model`：內嵌式 SQL 資料庫遷移引擎、外部鍵完整性與串聯關聯檢查、核心領域實體與資料模型定義。
- `internal/provider`：多郵件服務商抽象層標準介面，以及針對 Purelymail、Migadu 與標準手動 IMAP/SMTP 協定的驅動配接器。
- `internal/purelymail`：專為 Purelymail 管理 API 打造的底層傳輸用戶端與結構化錯誤包裝。
- `internal/core`：核心領域業務服務，涵蓋郵件連線管理、資源自動探索與匯入、組織權限控制、信箱完整生命週期、持久化管理操作（Operation）與信件遞送管線（Submission）。
- `internal/mailproto/imappool`：具備配額上限保護的 IMAP 連線複用池、以版本號（Revision）驅動的主動連線無效化機制及高效的 IMAP IDLE 即時監聽器。
- `internal/mailproto/mailops`、`mimeutil`、`sieve`：底層 IMAP/SMTP 協定通訊；MIME 格式郵件安全淨化與附件串流；符合 RFC 規範的 Sieve 篩選規則編譯器及 ManageSieve 用戶端。
- `internal/httpapi`、`internal/web`、`web/`：基於工作階段 Cookie 的身分驗證中介軟體、RESTful JSON 介面與 Server-Sent Events (SSE) 即時推播串流；內嵌前端靜態資產及現代化網頁信箱客戶端與管理主控台。

## 組織模型與資源歸屬

每個 Mailhearth 實例服務於一個獨立的團隊組織（`Organization`）。

1. **成員與角色（Member & Role）**：
   - 團隊成員包含個人基本資料（姓名、部門）、關聯角色及生命週期狀態（`invited` 待啟用、`active` 正常、`disabled` 停用、`departed` 已離職）。
   - 存取控制依循具名權限點（如 `mailboxes.manage`、`domains.manage` 等）組成的精細化角色。
   - **安全不變量**：管理權限絕不賦予郵件內文讀取權限。個人信箱（Personal Mailbox）的所有權與共用信箱（Shared Mailbox）的明確授權（`full` 完整存取、`send` 可讀可寄、`read` 唯讀）是存取郵件資料的唯一授權依據。

2. **郵件連線與網域綁定（MailConnection & DomainBinding）**：
   - `MailConnection` 隸屬於組織，持有服務商類型、連線名稱、管理 API 權杖/存取密鑰、允許的網域範圍及三大核心協定的預設範本。
   - `Domain` 代表組織的邏輯網域；`DomainBinding` 將網域連結至特定郵件連線，追蹤服務商特定設定及即時 DNS 解析健全度（MX、SPF、DKIM、DMARC）。
   - 信箱地址的唯一性範圍嚴格限制於所屬的郵件連線內部，不同連線下設定相同的信箱地址完全彼此獨立、互不衝突。

3. **地址拓撲與寄件身分（Addresses & Identities）**：
   - 地址支援多種業務類型：主地址（`primary`）、別名（`alias`）、轉寄（`forward`）、郵件群組（`group`）、全收（`catchall`）、前綴規則（`prefix`）及外部管理規則（`external_rule`）。
   - 信箱轉寄規則獨立儲存於 `mailbox_forwardings` 表中，匯入時會自動保留其所屬的主信箱歸屬。
   - 寄件身分（Identity）的呈現設定與 SMTP 寄件授權嚴格脫鉤：擁有收信別名並不代表獲得使用該別名作為發信人（From）的權限，必須明確取得授權。
   - 郵件群組發信清單根據部門或規則動態解析所有合資格的個人信箱，在遞送前自動校驗連線與網域限制，並精確消除重複目標。

4. **遠端資源對應（ProviderResource）**：
   - 將郵件服務商的每個遠端實體參照（如遠端信箱 ID、別名 ID）一對一對應至本地物件與業務用途。
   - 透過版本號（Revision）校驗與關聯條件保護，防止跨組織或跨連線的邊界洩漏，確保歷史稽核紀錄的真實完整。

## 協定設定與憑證隔離

每個信箱（Mailbox）均具備獨立的 IMAP、SMTP 及 ManageSieve 三大協定端點（Endpoint）：
- **網路模式（Network Mode）**：各端點支援設定為 `inherit`（繼承連線範本）、`override`（自訂獨立主機與連接埠）或 `disabled`（明確停用）。
- **憑證模式（Credential Mode）**：支援 `managed`（由平台自動產生與輪換管理憑證）或 `entered`（手動輸入既有密碼）。啟用的端點各自儲存加密憑證。
- **不可部分完成之驗證保證**：更新端點設定時，系統會在背景預先對所有啟用的協定執行連線驗證信號交換，唯有所有協定均順利通過驗證，方會在單一本機交易中提交變更。
- **連線平順無效化**：一旦端點組態、憑證或存取授權版本發生異動，IMAP 連線池會立即關閉舊連線，強制後續請求重新建立安全通道。
- **服務商預設範本**：
  - Purelymail：預設啟用 IMAP (`imap.purelymail.com:993` TLS)、SMTP (`smtp.purelymail.com:465` TLS) 及 ManageSieve (`mailserver.purelymail.com:4190` STARTTLS)。
  - Migadu：預設啟用 IMAP (`imap.migadu.com:993` TLS) 與 SMTP (`smtp.migadu.com:465` TLS)，ManageSieve 預設停用。
  - 手動連線：所有協定端點預設皆處於停用狀態，須由管理員依需求手動設定。
  - TLS 憑證驗證：預設依循作業系統內建的信任 CA 憑證鏈，並支援明確載入私有 CA 憑證包，嚴格檢驗伺服器主機名稱（SNI）。

## 資源探索、匯入與持久化管理操作

1. **資源探索與選擇性匯入**：
   - 資源探索（Discovery）會完整讀取指定連線下的遠端全量資源，產生帶有過期時效的快照，並綁定當前連線之 Revision。
   - 執行匯入時，系統在單一交易內嚴格驗證歸屬關係、版本號、有效期及相依限制。匯入的個人信箱預設處於未指派狀態（無 Owner 且無本地登入憑證），待後續分派。
   - 同步（Sync）作業會安全更新已登記資源之觀測狀態，新增的遠端資源保持待匯入清單；系統會完整保留本地已分派的 Owner、存取權限、協作備註、簽名檔及手動憑證。
   - 轉寄匯入完整保留來源信箱、目標確認狀態（`active`、`pending_confirmation`、`blocked`、`unknown`）與遠端參照。未驗證的轉寄保持 `unverified`，其中 Migadu 轉寄寫入受嚴格的投遞驗證保障。

2. **持久化非同步操作引擎（Operation Engine）**：
   - 涉及遠端服務商通訊或長時間執行的管理作業均透過 `Operation` 實體執行，追蹤唯一的 `requestId`、內容指紋、加密負載、資源互斥鎖及各步驟進度。
   - **嚴格冪等性**：相同 `requestId` 的重複請求將直接冪等傳回既有操作；若組態版本變更或授權失效，系統將立即中止舊任務執行。
   - **狀態未明隔離**：遠端寫入若遭遇連線逾時或未知回應，操作會明確進入待查核狀態，已確認完成的步驟絕不盲目重複執行。
   - **外部管理員報告**：當特定步驟須於服務商管理介面手動處理時，系統接受管理員回報並標記為 `systemVerified=false`；建立憑證若於回應中斷且無遠端 ID，必須於清理後提出正式報告。
   - **離職交接流程（Offboarding）**：以持久化作業完整記錄信箱移交或封存、群組關係移除、本機會話終止及外部憑據撤銷全流程。

## 郵件處理與安全渲染管線

1. **權限解析與連線調度**：
   - HTTP 請求抵達後，中介層即時解析呼叫者身分、會話狀態及信箱存取權限，動態指定作用中之協定端點。
   - IMAP 連線池實施三層配額管理：全域預設上限 24、單一連線上限 8、單一信箱上限 3。當背景 IDLE 監聽器運作時，連線池固定保留全域 4 個、單一連線 2 個名額優先供前台互動請求使用。

2. **信箱讀取與特殊資料夾判斷**：
   - `mailops` 套件透過 IMAP 指令執行資料夾列舉、分頁瀏覽、伺服器端搜尋及 MIME 結構解析。
   - 特殊資料夾判定依循嚴格優先序：明確指定之對應設定 > RFC 6154 `SPECIAL-USE` 標籤 > 唯一標準英文字詞/本地化名稱。

3. **內容沙箱安全防護**：
   - 郵件 HTML 內文於伺服器端由 `mimeutil` 執行深層淨化，剝除可執行指令碼、危險樣式與追蹤標籤。內嵌圖片與附件重新解析對應至內部受控的 `cid:` 端點。
   - 郵件呈現於嚴格 Content Security Policy (CSP) 防護下的無指令碼沙箱 `<iframe>` 中，外部遠端圖片預設全面封鎖，防止發信者刺探使用者 IP 與讀取狀態。

4. **高可靠寄件狀態機（Submission Pipeline）**：
   - 寄送請求於資料庫中持久化紀錄：指派全域唯一且固定的 `Message-ID`、內容指紋及加密信封；完整郵件內文預先暫存於伺服器 Drafts 草稿匣中。
   - **步驟獨立解耦**：SMTP 接受投遞與寄件備份（Sent Copy）儲存作為獨立步驟個別追蹤。遞送結果未明時絕不自動重試以避免重複寄送；Sent 備份失敗時僅重試副本寫入，絕不重複提交 SMTP。草稿清理透過精準之 UID EXPUNGE 完成。

5. **即時團隊協作**：
   - 共用信箱藉由 IMAP IDLE 監聽器感知新進郵件，並透過 Server-Sent Events (SSE) 即時推播至瀏覽器。
   - 團隊協作脈絡統一錨定於 `mid:<message-id>`。`message_state` 追蹤負責人與結案狀態，`mail_activity` 完整記錄回覆、轉寄、指派與內部備註，不受郵件資料夾移動影響。

## 郵件規則系統與前端架構

1. **Sieve 郵件篩選與自動回覆**：
   - 結構化篩選規則在儲存時，會依據伺服器透過 ManageSieve 所宣告之擴充能力，動態編譯為標準 Sieve 指令碼。
   - 部署規則前，系統會先比對伺服器上作用中指令碼的雜湊值；接管既有指令碼須獲明確授權確認，確保不會意外覆蓋舊有設定。

2. **現代化單頁應用程式架構（SPA）**：
   - 前端採用 Preact 與 `@preact/signals` 驅動響應式狀態，以輕量級 History Router 管理 `/mail`、`/admin`、`/settings`、`/login`、`/invite/:token` 與 `/setup` 等頁面。
   - 響應式排版：螢幕寬度大於 860px 時呈現標準三欄工作區（資料夾、郵件清單、內文閱讀器），行動裝置與窄螢幕自動摺疊為單欄搭配抽屜導覽。
   - **多語言架構**：以英文為原始詞典，完整提供簡體中文（zh-CN）、繁體中文（zh-TW）、日文（ja）及西班牙文（es）四組語言字典。測試流程透過 TypeScript 抽象語法樹（AST）自動化驗證所有文字的覆蓋完整性與變數插值對稱性。
