# 複数プロバイダーの結合テスト

[English](integration-testing.md) · [简体中文](integration-testing.zh-CN.md) · [繁體中文](integration-testing.zh-TW.md) · **日本語** · [Español](integration-testing.es.md)

## ローカル検証

リポジトリのルートから実行します。キャッシュと中間ファイルは Git が無視する `data/` に保存します。

```powershell
$env:GOCACHE=Join-Path (Get-Location) 'data/go-build-cache'
$env:GOTMPDIR=Join-Path (Get-Location) 'data/integration/go-work'
New-Item -ItemType Directory -Force $env:GOCACHE,$env:GOTMPDIR | Out-Null
$env:TEMP=$env:GOTMPDIR
$env:TMP=$env:GOTMPDIR
go test ./... -run '^$'
gofmt -l ./internal ./cmd
go test -count=1 ./... -timeout 180s
go test -count=1 ./... -run '^TestMultiProvider' -timeout 120s
go vet ./...
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
git diff --check
```

全パッケージのコンパイル検証は `-run '^$'` を使用します。選択テストは実際の SQLite と
アプリの HTTP server、純粋なアルゴリズムで所属、認可、操作、ルーティング、転送の取り込み・
観測、送信状態、Sieve を検証します。フロントエンドは TypeScript parser で requestId の
安定性、翻訳の網羅性、補間パラメーターを確認します。外部配信とブラウザー操作は対象外です。
並行処理や転送は `-count=20` で反復できます。race 検証には C コンパイラーと cgo が必要です。

## 実環境と安全性

`internal/integration/multiprovider` は実際の API とプロトコル接続を使用します。
`MAILHEARTH_DEV_STACK` を無効にしてください。設定パスが未指定の場合は明示的にスキップし、指定した設定が無効な場合は失敗します。
専用アカウント・ドメインと重要なメールを含まないメールボックスを使用します。プロトコル
テストは実メールを送信し、独立したローカルテスト配置の endpoint 設定を変更します。
実行前に対象テストを読んでください。現在の前提・プロトコルテストは既存の外部パスワードを
リセットしません。今後のライフサイクル検証は作成・削除・認証情報失効・ルール変更を含む
可能性があり、専用リソースが必要です。

設定は `data/integration/multi-provider/` にのみ保存します。パスは解決後に所属を検証します。
認証情報を Git、チャット、ログに入れないでください。各実行はここに独立データベース、
マスターキー、テストデータと報告を保存します。安全に管理し、必要に応じて後処理してください。

## 設定

JSON は未知フィールドと余分な JSON 値を拒否します。すべての環境を指定します。

| フィールド | 必須内容 |
|---|---|
| `purelymail` | `apiKey`、`domain`、`imap`、`smtp`、`managesieve` |
| `migadu` | 上記に加えて `apiUsername`。ManageSieve がない場合は明示無効化 |
| 各プロトコル設定 | `enabled`。有効時は `host`、`port`、`tlsMode`（`tls`/`starttls`）、任意の `caBundleId` |
| `manual` | `primaryMailbox`、`secondaryMailbox`、`independentSmtp`、`privateCaMailbox`、`noSieveMailbox` |
| 各手動メールボックス | `address`、`credentials`（`clientKey`、`secret`）、三つのプロトコルを含む `endpoints` |
| 有効な手動 endpoint | `networkMode: override`、`network`、`authMode: password`、`username`、`credential: {clientKey}` |
| 無効な手動 endpoint | `networkMode: disabled` |
| `deliveryTimeoutSeconds` | 正の整数。既定 180 |

`independentSmtp` の IMAP と SMTP は異なる username と secret が必要です。プライベート
CA はテスト配置から利用可能にし、`caBundleId` を設定します。`MAILHEARTH_CA_BUNDLES_FILE`
は正の CA ID を PEM パスに関連付ける JSON ファイルです。`noSieveMailbox` は
ManageSieve を無効にし、IMAP/SMTP を利用可能にします。管理 API 認証はメールのパスワードを提供しません。

```powershell
$env:MAILHEARTH_MULTIPROVIDER_TEST_CONFIG=Join-Path (Get-Location) 'data/integration/multi-provider/config.json'
go test -count=1 -v ./internal/integration/multiprovider -timeout 40m
go test -count=1 -v ./internal/purelymail ./internal/mailproto/mailops -timeout 40m
```

## 範囲と結果

- `TestRealMultiProviderPrerequisites`：Purelymail/Migadu の実管理認証と手動プロトコル。
  成功後に `prerequisites.json` を保存します。
- `TestRealMultiProviderTransactions`：接続隔離、同一アドレス登録、候補拒否、revision
  競合、requestId と内容、安全な公開フィールド。T01、T03、T08、T17–T19、T40 の一部に
  対応し、`transactions.json` を保存します。
- `TestRealMultiProviderProtocols`：独立認証と実配信（T04）、SMTP 無効時の読み取り
  （T05）、ManageSieve 無効時の読み取り・配信（T06）、endpoint 変更と古い接続の終了
  （T37）。成功後に `protocols.json` を保存します。
- `TestClientAgainstRealPurelymail`：実アカウントの参照、ユーザー・パスワード・
  routing rule 操作と、本テストで作成したリソースの IMAP 認証・失効確認。
- `TestMailOpsAgainstRealProtocols`：フォルダー、ページング、検索、flags、COPY/MOVE/削除、
  MIME、添付、SMTP 配信と IDLE。独立フォルダーと一意のメッセージ識別子を使用します。
- `TestListFoldersOnRev1Server`：実 IMAP4rev1 の LIST、STATUS と特殊フォルダー。
  `manual.primaryMailbox` に IMAP4rev2、LIST-EXTENDED、LIST-STATUS、SPECIAL-USE が
  ないことが必要です。他のサーバーではプロトコルテストを名前で選択してください。

報告は `acceptanceComplete=false` です。T01–T40 と V01–V07 の全項目はテスト追加と
実行が必要です。`internal/integration` の既存 Purelymail スイートは現行 API への移行が
必要です。ローカル成功や取り込み成功は受け入れ完了を意味しません。Migadu の転送方式は
V03 成功まで `unverified` です。

配信失敗時は MX/SPF、認証情報、有効プロトコル、宛先確認、Junk 等のフォルダーと
Message-ID を確認します。ネットワークエラーは失効や削除の証明になりません。
operationId/submissionId を保持して不明な結果を照合し、自動再送しないでください。
通常 CI に実認証情報を入れず、プロバイダー、SMTP/IMAP、migration、Sieve の変更後と
リリース前に専用の受け入れ検証を行います。
