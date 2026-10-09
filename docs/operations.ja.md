# 運用

[English](operations.md) · [简体中文](operations.zh-CN.md) · [繁體中文](operations.zh-TW.md) · **日本語** · [Español](operations.es.md)

## サイジング

Mailhearth は、購入できる最小のホスト向けに作られています。

| リソース | 目安 | 備考 |
|---|---|---|
| バイナリ | 約 25 MB 静的 | cgo なし、ランタイム依存なし |
| アイドル時 RSS | 25–40 MB | `GOMEMLIMIT` の既定値は 160 MiB |
| ビジー時 RSS（10 ユーザー） | 60–120 MB | 大半は IMAP 取得バッファー |
| CPU | 無視できる | ログイン時の argon2id が最も重い処理 |
| ディスク | 組織データに応じて増加 | SQLite に操作、送信状態、共同作業情報を保存。メールは各 IMAP サービスに保存 |
| フロントエンド | gzip 後約 100 KB | 2026-10-09 の JS と CSS のビルド結果。ファイル名は内容ハッシュを含む |

`MAILHEARTH_IMAP_MAX_CONNS`（既定 24）は「同時利用ユーザー数 × 2 に、利用者が開いたままにしているメールボックスの数を足した値」を目安に調整します。各 IDLE ウォッチャーは 1 本の接続を保持します。全体の既定上限は 24、connection ごとの上限は 8、mailbox ごとの上限は 3 です。IDLE は通常リクエスト用の接続枠を残します。各プロバイダーの実際の制限に従って設定します。

## バックアップ

状態のすべてはデータディレクトリです。

| パス | 内容 |
|---|---|
| `/data/mailhearth.db` | SQLite データベース（WAL モード） |
| `/data/mailhearth.db-wal` | 先行書き込みログ。データベースと一緒にコピーする |
| `/data/master.key` | 保存済み認証情報を暗号化する鍵 |
| `/data/uploads/` | 送信待ちの作成中添付ファイル（一時的） |

コンテナを停止してバックアップするか、`sqlite3 mailhearth.db ".backup out.db"` で一貫性のあるオンラインコピーを取得します。`master.key` は別の安全な場所に保管します。新しいホストでの復元は、両方のファイルを置いてバイナリを起動するだけです。

## アップグレード

migration は起動時に実行され、テーブル変換と参照検査を含みます。更新前にデータベースと master key の整合したバックアップを保存します。新しいイメージを取得し、`docker compose up -d` を実行します。移行をまたぐダウングレードはサポートしていません。代わりにバックアップから復元してください。

## リバースプロキシ

Caddy：

```caddyfile
mail.example.com {
    reverse_proxy 127.0.0.1:8080 {
        flush_interval -1      # required for Server-Sent Events
    }
}
```

nginx：`/api/mail/` に `proxy_buffering off;` と `proxy_read_timeout 3600s;` を設定して SSE ストリームがバッファリングされないようにし、添付ファイル用に `client_max_body_size 50m` を設定します。

## トラブルシューティング

**`provider_auth_failed`** — 管理 → 接続で該当する connection の管理認証を更新します。候補の認証に失敗した場合、現在の設定を保持します。メール認証は別に検査します。

**メールプロトコルが未設定** — インポートは選択したリソースを登録します。IMAP、SMTP、ManageSieve と entered 認証を明示的に設定するか、connection の managed 認証機能を使用します。有効なプロトコルの実際の認証後に設定を保存します。SMTP と ManageSieve は独立して無効化できます。

**`mailbox_auth_failed`** — 該当プロトコルの username と password を確認します。entered 認証はプロトコル設定、managed 認証はローテーション操作で更新します。ネットワーク障害、一時的な拒否、未分類の ManageSieve 応答は未確認のまま保持します。遠隔認証の失効は新しい接続で確認します。

**ルールを保存できない** — mailbox の ManageSieve endpoint、TLS、拡張を確認します。Purelymail の既定は `mailserver.purelymail.com:4190` と STARTTLS です。Migadu のテンプレートは無効で開始し、実際のアカウント設定を使用します。既存の active script の引き継ぎには明示的な確認と現在の内容ハッシュが必要です。自動返信には `vacation` が必要です。

**Operation が `unknown`** — ステップと遠隔結果を確認してから明示的に再試行します。リソースロックは保持されます。認証作成の応答が失われて遠隔 ID がない場合、プロバイダー画面で認証を処理し、管理者の清理報告を提出します。元の操作は取り消され、作成済みリソースは保持されます。状態は `external_reported`、`systemVerified: false` です。

**Submission が `sent_copy_failed`** — SMTP はメールを受け付けています。Sent コピーのみ再試行します。SMTP または APPEND が `unknown` の場合、Submission と固有識別子を確認し、元の requestId を保持します。

**同期で新しいリソースを検出** — connection の同期結果からインポート対象を選択します。認証、権限、履歴を保持します。読み取り失敗から削除済みと判定しません。

**ライブ更新がない** — SSE がプロキシでバッファリングされています。上記を参照してください。クライアントは手動更新に切り替わり、操作が起きたときには引き続きフォルダーをポーリングします。

**巨大なフォルダーの最初のページが遅い** — 一覧表示は 50 通のエンベロープと本文構造を取得する単一のシーケンス範囲 FETCH です。Purelymail のサーバー側検索インデックス（ユーザー単位で有効化）により検索は高速になります。Mailhearth が作成するメールボックスでは有効になっています。

## ログ

構造化テキストログを stderr に出力します。`MAILHEARTH_LOG_LEVEL=debug` を設定すると、リクエストごとの行と IMAP ウォッチャーの再接続が追加されます。ログに認証情報は含まれません。

## 検査コマンド

```powershell
go test ./... -run '^$'
go test -count=1 ./internal/db ./internal/core ./internal/httpapi ./internal/mailproto/sieve ./internal/provider -run '^TestMultiProvider'
go vet ./...
npm --prefix web run test
npm --prefix web run build
```

実環境テストでは `MAILHEARTH_DEV_STACK` を無効化します。認証設定をプロジェクトの `data/integration/multi-provider/` に保存して `MAILHEARTH_MULTIPROVIDER_TEST_CONFIG` を設定し、`go test -count=1 -v ./internal/integration/multiprovider` を実行します。設定がなければ明示的に失敗します。ローカル検査と実環境の検証は別に記録します。未実行の検証項目は未完了として保持します。
