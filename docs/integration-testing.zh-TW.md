# 對真實 Purelymail 帳戶的整合測試

[English](integration-testing.md) · [简体中文](integration-testing.zh-CN.md) · **繁體中文** · [日本語](integration-testing.ja.md) · [Español](integration-testing.es.md)

單元測試執行在 Purelymail API 的記憶體內假實作上。這個假實作證明
Mailhearth 自身一致，但無法證明 Mailhearth 與實際服務相符：假實作與用戶端
出自對 API 的同一份解讀。只有真實帳戶才能找出錯誤的欄位名稱、Purelymail
解讀方式與我們假設不同的規則、實際上沒有被撤銷的應用程式密碼，或是永遠
不會送達的郵件。

`internal/integration` 裡的測試套件補上這個缺口。它預設會被略過，因此
`make test` 與 CI 維持離線且快速。

## 涵蓋範圍

每個測試都會驅動 Mailhearth 自己的服務層，接著把結果拿去和實際帳戶比對；
Mailhearth 的資料庫不參與比對。

| 測試 | 證明的內容 |
| --- | --- |
| `TestImportExistingAccount` | 連線到一個已經存有使用者與規則的帳戶時，會忠實地匯入它們、將它們標記為已匯入、不簽發任何應用程式密碼，且不變更上游任何內容。第二次同步不會有任何作用。 |
| `TestMailboxLifecycle` | 建立信箱會建立一個具有可用應用程式密碼的真實使用者；IMAP 登入、SMTP 投遞、送達、呈現以及寄件備份都能運作；輪替會讓先前的密碼在供應商端失效；刪除會移除該使用者。 |
| `TestRoutingRules` | 別名會成為供應商遵守的路由規則，寄到該別名的郵件會送達，而刪除該地址會撤回該規則。 |
| `TestGroupDistribution` | 群組地址會送達每一位成員，而移除成員會在上游改寫該規則。 |
| `TestSharedMailboxAccess` | 授權決定誰可以開啟共用信箱。只有管理員權限永遠不會授予郵件存取權。撤銷授權就會關上這扇門。 |
| `TestSieveRules` | Mailhearth 編譯出的 Sieve 指令碼會被供應商接受、成為作用中的指令碼，並且真的把郵件歸入目標資料夾，收件匣收不到該郵件。 |
| `TestOffboarding` | 離職的人會失去每一種進入方式，包括他們桌面郵件用戶端持有的憑證，而他們的信箱與其歷史會轉移給接任者。 |
| `TestSuspendAndReactivate` | 暫停會讓所有人無法進入，但仍然繼續接收郵件；重新啟用會恢復存取，而期間送達的郵件就在那裡。 |
| `TestPasswordResetForExternalClients` | 發給 Thunderbird 的密碼真的能通過驗證，而 Mailhearth 自己的應用程式密碼能度過重設，並與它保持不同。 |
| `TestExternalForwarding` | 轉寄到帳戶外部地址的設定會成為我們預期的規則。 |
| `TestTokenRejection` | 錯誤的 API 權杖會被拒絕，而且不會損壞已儲存且可用的連線。 |

## 安全性

測試套件會建立並刪除真實的信箱與真實的路由規則，而刪除 Purelymail 使用者
會連帶刪除其郵件。有兩套機制把這件事情限制在可控的範圍內。

測試套件建立的每個物件都命名為 `<prefix>-<runid>-<role><n>`，其中前置字元
預設為 `mh-it`。任何具破壞性的輔助函式在碰觸某個地址之前，都會呼叫
`guardOwned`；除非該地址位於設定的測試網域**而且**帶有該前置字元，否則
它會中止整次執行。測試無法刪除自己沒有建立的信箱，即使某個程式碼變更
要求它這麼做。

每個測試在建立物件時也會把它們登記為待清理項目，因此中斷或失敗的執行
仍然會拆除自己建立的物件。

即使如此：**請把測試套件指向一個不存放任何你在意內容的網域。** 在該帳戶上
使用專用的測試網域才是正確的做法。這些防護措施防範的是測試套件行為失常；
`MAILHEARTH_IT_DOMAIN` 打錯字而剛好寫成你的正式網域與前置字元，防護措施
幫不上忙。

每個測試都會在暫存目錄中建立自己的空白資料庫與自己的主要金鑰，因此不會
碰觸你真正的安裝。

## 執行方式

該網域必須已經存在於 Purelymail 帳戶中，並具有可用的 MX 記錄，因為送達
正是大多數這類測試所衡量的重點。API 權杖需要完整存取權限。

```bash
export MAILHEARTH_IT_TOKEN=your-api-token
export MAILHEARTH_IT_DOMAIN=test.example.com

go test ./internal/integration -v -timeout 40m
```

整套測試預期會花上數分鐘：大部分時間都花在等待郵件真正送達。迭代期間可以
只執行單一測試：

```bash
go test ./internal/integration -run TestMailboxLifecycle -v -timeout 15m
```

這些測試會在帳戶上建立並刪除使用者。Purelymail 依使用者計費，所以完整執行
一次的費用是幾分之一分錢。

## 設定

| 環境變數 | 預設值 | 用途 |
| --- | --- | --- |
| `MAILHEARTH_IT_TOKEN` | — | API 權杖。必填；沒有它測試套件會略過。 |
| `MAILHEARTH_IT_DOMAIN` | — | 測試網域。必填；沒有它測試套件會略過。 |
| `MAILHEARTH_IT_PREFIX` | `mh-it` | 標示測試套件擁有物件的本地部分前置字元。必須以 `mh` 開頭。 |
| `MAILHEARTH_IT_EXTERNAL` | — | 帳戶外部的地址。啟用 `TestExternalForwarding`。 |
| `MAILHEARTH_IT_DELIVER_SECONDS` | `180` | 失敗之前等待郵件送達的時間長度。 |
| `MAILHEARTH_IT_KEEP` | 未設定 | 保留建立的物件以供檢查。你必須自己清理它們。 |
| `MAILHEARTH_IT_API_URL` | `https://purelymail.com/api/v0` | API 端點。 |
| `MAILHEARTH_IT_IMAP_ADDR` | `imap.purelymail.com:993` | IMAP 地址。 |
| `MAILHEARTH_IT_SMTP_ADDR` | `smtp.purelymail.com:465` | 提交地址。 |
| `MAILHEARTH_IT_SIEVE_ADDR` | `mailserver.purelymail.com:4190` | ManageSieve 地址。留空會略過 Sieve 測試。 |
| `MAILHEARTH_IT_IMAP_TLS` | `tls` | `tls`、`starttls` 或 `none`。 |
| `MAILHEARTH_IT_SMTP_TLS` | `tls` | `tls`、`starttls` 或 `none`。 |
| `MAILHEARTH_IT_SIEVE_TLS` | `starttls` | `tls`、`starttls` 或 `none`。 |

## 測試失敗時

送達逾時是最常見的失敗，通常代表該網域的 DNS 不完整，Mailhearth 本身並
沒有壞掉。請先檢查 MX 與 SPF 記錄，然後調高
`MAILHEARTH_IT_DELIVER_SECONDS`。Purelymail 的垃圾郵件篩選也可能把測試郵件
歸入垃圾郵件資料夾；失敗訊息會指出它搜尋的資料夾，以及在該資料夾看到多少
封郵件。

使用 `MAILHEARTH_IT_KEEP=1` 重新執行，可以把物件留在原處，並在 Purelymail
網頁介面中檢查它們。記得之後要刪除它們，因為它們會持續產生費用，而且
下一次執行並不會接管它們。

如果某次執行被強制終止到來不及清理，殘留的物件很容易找到：每一個都帶有
該前置字元。

## 讓它留在 CI 之外

不要把這個測試套件接進提取要求流程。它需要真實的權杖、會花錢，而且等待
送達的時間讓它慢到不適合當成合併前的關卡。請在發行版本之前執行它，並在對
Purelymail 用戶端、IMAP 或 SMTP 路徑，或 Sieve 編譯器做出任何變更之後
執行。
