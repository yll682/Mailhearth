# 更新日誌

[English](CHANGELOG.md) · [简体中文](CHANGELOG.zh-CN.md) · **繁體中文** · [日本語](CHANGELOG.ja.md) · [Español](CHANGELOG.es.md)

Mailhearth 所有值得注意的變更都記錄在本文件中。

格式遵循 [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)，
且本專案採用 [Semantic Versioning](https://semver.org/spec/v2.0.0.html)。

## [未發布]

### 新增

- 繁體中文（台灣）、日文與西班牙文的介面翻譯，可從登入畫面與設定中
  選擇。

## [0.1.0] - 2026-09-20

首次公開發行版本：可自行架設的商務電子郵件平台，可將 Purelymail
帳號變成團隊郵件產品。

### 新增

- **首次執行設定**：建立組織、驗證 Purelymail API 權杖，並匯入現有的
  網域、信箱與路由規則，且不變更帳號上的任何內容。匯入為唯讀且可
  重複執行。
- **組織模型**：成員具有職稱、部門、角色與狀態（已邀請、使用中、已
  停用、已離職）；內建的擁有者、管理員與成員角色，以及由精細權限
  組合而成的自訂角色；管理動作與擁有權移轉的稽核記錄。
- **信箱生命週期**：個人信箱與共用信箱，`full`、`send` 與 `read` 層級
  的存取授權，每個信箱一組由伺服器建立並輪換的應用程式密碼，以及
  一步完成的離職處理，可移交信箱、將其轉為共用、保留或鎖定、轉寄
  新郵件、輪換所有憑證，並將成員從群組中移除。
- **地址**：主要地址、別名、轉寄、全收地址與前綴規則，以及跟隨群組
  成員身分的群組發送地址。
- **網域**：網域管理，包含 MX、SPF、DKIM 與 DMARC 的 DNS 健康狀態，
  以及可直接複製貼上的 DNS 記錄。
- **網頁信箱**：資料夾、分頁、伺服器端搜尋、標記、批次操作、附件、
  自動儲存的草稿、簽名檔與多個寄件身分、透過 IMAP IDLE 的桌面通知、
  鍵盤快速鍵、淺色與深色模式、英文與簡體中文。
- **共用信箱協作**：查看誰已回覆、指派郵件、標示為已解決，並留下
  內部備註。協作狀態以 `Message-ID` 作為索引鍵，因此可在資料夾之間
  搬移後繼續保留。
- **郵件規則**：結構化的條件與動作，以及休假自動回覆，會編譯為
  Sieve 並透過 ManageSieve 安裝，因此即使沒有人登入也能持續運作。
- **安全性**：Purelymail API 權杖與每個信箱的應用程式密碼，在靜態時
  以 AES-256-GCM 加密，金鑰由主金鑰衍生而成，且永遠不會離開伺服器；
  argon2id 密碼雜湊、CSRF 防護、具速率限制的登入；郵件 HTML 在伺服器
  端清理，並在嚴格的 CSP 下以沙箱化、不含指令碼的 iframe 呈現，遠端
  圖片預設阻擋；附件以 `nosniff` 與下載處置方式提供。
- **部署**：單一靜態 Go 執行檔，內嵌網頁用戶端，透過純 Go 的
  `modernc.org/sqlite` 驅動程式使用 SQLite 儲存，提供 `Dockerfile` 與
  `docker-compose.yml`，以及 GitHub Actions 工作流程，負責執行測試套件、
  建置執行檔，並將多架構映像檔發布到 GHCR。
- **開發與驗證**：Purelymail API、IMAP 與 SMTP 的行程內模擬實作，附
  示範資料（`make dev`）；驅動真實介面的螢幕擷取腳本；以及對真實
  Purelymail 帳號執行的整合測試套件。

[Unreleased]: https://github.com/yll682/Mailhearth/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/yll682/Mailhearth/releases/tag/v0.1.0
