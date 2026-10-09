# 多供應商整合測試

[English](integration-testing.md) · [简体中文](integration-testing.zh-CN.md) · **繁體中文** · [日本語](integration-testing.ja.md) · [Español](integration-testing.es.md)

## 本地檢查

在專案根目錄執行。快取和中間檔案保存於已忽略的 `data/` 目錄。

```powershell
$env:GOCACHE=Join-Path (Get-Location) 'data/go-build-cache'
$env:GOTMPDIR=Join-Path (Get-Location) 'data/integration/go-work'
New-Item -ItemType Directory -Force $env:GOCACHE,$env:GOTMPDIR | Out-Null
go test ./... -run '^$'
go test -count=1 ./... -run '^TestMultiProvider' -timeout 120s
go vet ./...
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
git diff --check
```

全部套件的編譯檢查使用 `-run '^$'`。指定測試使用真實 SQLite、應用 HTTP server 和
純演算法，檢查關聯歸屬、授權、操作、路由、轉寄匯入與觀察、寄送狀態和 Sieve。
前端測試使用 TypeScript parser 檢查 requestId 穩定性、翻譯覆蓋與插值參數。這些
檢查沒有執行外部投遞或瀏覽器互動。並行與轉寄檢查可使用 `-count=20` 重複執行；
race 檢查需要 C 編譯器及 cgo。

## 真實環境與安全

`internal/integration/multiprovider` 使用真實 API 和協定連線。關閉
`MAILHEARTH_DEV_STACK`；缺少設定時明確失敗。使用專用測試帳戶、網域及沒有重要
郵件的信箱。協定測試寄送真實郵件，並修改獨立本地測試部署的 endpoint 設定。
執行前閱讀所選測試；目前驗證前提和協定檢查不重設既有外部信箱密碼。後續生命週期
驗收可能建立、刪除資源、撤銷憑證和修改規則，需要使用專用資源。

設定只保存於 `data/integration/multi-provider/`，程式解析路徑並檢查所屬目錄。
憑證不提交到 Git，也不傳送到聊天或記錄。每次執行在該目錄下保存獨立資料庫、主金鑰、
測試資料和報告。妥善保護這些檔案，完成檢查後按需要清理。

## 設定

JSON 讀取拒絕未知欄位和額外 JSON 值。以下環境全部需要設定：

| 欄位 | 必要內容 |
|---|---|
| `purelymail` | `apiKey`、`domain`、`imap`、`smtp`、`managesieve` |
| `migadu` | 上述欄位及 `apiUsername`；沒有 ManageSieve 時明確停用 |
| 各協定範本 | `enabled`；啟用時提供 `host`、`port`、`tlsMode`（`tls`/`starttls`），可選 `caBundleId` |
| `manual` | `primaryMailbox`、`secondaryMailbox`、`independentSmtp`、`privateCaMailbox`、`noSieveMailbox` |
| 各手動信箱 | `address`、`credentials`（`clientKey`、`secret`），及包含三項協定的 `endpoints` |
| 啟用的手動 endpoint | `networkMode: override`、`network` 範本、`authMode: password`、`username`、`credential: {clientKey}` |
| 停用的手動 endpoint | `networkMode: disabled` |
| `deliveryTimeoutSeconds` | 正整數，預設 180 |

`independentSmtp` 的 IMAP 和 SMTP 必須使用不同 username 和 secret。私人 CA 檢查
需要測試部署可以讀取的 CA 及對應 `caBundleId`。`MAILHEARTH_CA_BUNDLES_FILE` 指向
將正整數 CA ID 對應至 PEM 檔案路徑的 JSON 檔案。`noSieveMailbox` 停用 ManageSieve，
保留可用的 IMAP/SMTP。供應商管理驗證不提供信箱協定密碼。

```powershell
$env:MAILHEARTH_MULTIPROVIDER_TEST_CONFIG=Join-Path (Get-Location) 'data/integration/multi-provider/config.json'
go test -count=1 -v ./internal/integration/multiprovider -timeout 40m
```

## 覆蓋與結果

- `TestRealMultiProviderPrerequisites`：真實 Purelymail/Migadu 管理驗證及手動協定
  憑證。成功後寫入 `prerequisites.json`。
- `TestRealMultiProviderTransactions`：連線隔離、相同地址登記、候選驗證拒絕、revision
  衝突、requestId 與內容檢查及安全查詢欄位。對應部分 T01、T03、T08、T17–T19、
  T40，成功後寫入 `transactions.json`。
- `TestRealMultiProviderProtocols`：獨立憑證與實際投遞（T04）、SMTP 停用後的讀取
  （T05）、ManageSieve 停用後的讀取與投遞（T06）、endpoint 版本及舊連線關閉
  （T37），成功後寫入 `protocols.json`。

報告保存 `acceptanceComplete=false`。完整 T01–T40 和 V01–V07 仍需要補充測試並
實際執行；`internal/integration` 的舊 Purelymail 套件仍需要遷移至目前介面。
本地測試通過或匯入成功不表示真實供應商驗收完成。Migadu 轉寄方式在 V03 通過前
保持 `unverified`。

投遞失敗時檢查 MX/SPF、憑證、啟用協定、目標確認狀態、Junk 等資料夾及 Message-ID。
網路錯誤不證明憑證已撤銷或資源已刪除。保留 operationId/submissionId 並查核未知
結果，不自動重新寄送。一般 CI 不使用真實憑證；供應商、SMTP/IMAP、migration
或 Sieve 變更後及發行前執行專用驗收。
